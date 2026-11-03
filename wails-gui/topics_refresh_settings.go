package main

import "time"

func (a *App) SetTopicRefreshInterval(minutes int) (int, error) {
	svc := a.topicsSvc()
	effective := minutes
	if effective == 0 {
		effective = int(topicBackgroundRefreshDefault.Minutes())
	} else {
		if effective < 5 {
			effective = 5
		}
		if effective > 120 {
			effective = 120
		}
	}
	_, err := svc.store.update(func(state *topicsStateFile) {
		if minutes == 0 {
			state.RefreshIntervalMinutes = 0
		} else {
			state.RefreshIntervalMinutes = effective
		}
	})
	if err != nil {
		return 0, err
	}
	svc.rescheduleRegistryRefresh()
	return effective, nil
}

func (s *topicsService) rescheduleRegistryRefresh() {
	interval := s.store.load().backgroundRefreshInterval()
	var snapshotAt time.Time
	if snapshot := s.snapshots.Current(); snapshot != nil {
		snapshotAt = snapshot.At
	}
	s.registryWorkerMu.Lock()
	if s.registryRefresh == nil {
		s.registryWorkerMu.Unlock()
		return
	}
	base := s.registryLastBuildStart
	if base.IsZero() {
		if snapshotAt.IsZero() {
			base = s.now()
		} else {
			base = snapshotAt
		}
	}
	next := base.Add(interval)
	if !s.registryWithdrawalRebuildAt.IsZero() && s.registryWithdrawalRebuildAt.Before(next) {
		next = s.registryWithdrawalRebuildAt
	}
	s.registryNextRefresh = next
	changed := s.registryScheduleChanged
	s.registryWorkerMu.Unlock()
	if changed != nil {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
}
