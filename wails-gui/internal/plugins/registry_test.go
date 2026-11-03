package plugins

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestRegisterDuplicateIDPanics(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Manifest{ID: "calendar"})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate ID did not panic")
		}
		if got := len(registry.Manifests()); got != 1 {
			t.Fatalf("duplicate changed catalog length to %d", got)
		}
	}()
	registry.Register(Manifest{ID: "calendar"})
}

func TestReconcileDependencyOrderAndReverseTeardown(t *testing.T) {
	registry := NewRegistry()
	var applied, disposed []string
	register := func(id string, requires, provides []string) {
		registry.Register(Manifest{
			ID: id, Requires: requires, Provides: provides,
			Apply: func(_ context.Context, scope *Scope) error {
				applied = append(applied, id)
				scope.OnDispose(func() { disposed = append(disposed, id) })
				return nil
			},
		})
	}
	register("card", []string{"events"}, nil)
	register("calendar", []string{"store"}, []string{"events"})
	register("store", nil, nil)
	enabled := map[string]bool{"card": true, "calendar": true, "store": true}
	for range 2 {
		if err := registry.Reconcile(context.Background(), enabled); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"store", "calendar", "card"}; !slices.Equal(applied, want) {
		t.Fatalf("apply order = %v, want %v (without duplicate applies)", applied, want)
	}
	enabled["store"] = false
	if err := registry.Reconcile(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
	if want := []string{"card", "calendar", "store"}; !slices.Equal(disposed, want) {
		t.Fatalf("dispose order = %v, want %v", disposed, want)
	}
	if got := registry.ActiveIDs(); len(got) != 0 {
		t.Fatalf("disabled provider left active plugins: %v", got)
	}
	if !enabled["card"] || !enabled["calendar"] {
		t.Fatal("reconciliation rewrote stored dependent preferences")
	}
}

func TestFailedApplyRemovesContributionsAndSkipsDependents(t *testing.T) {
	registry := NewRegistry()
	contributions := make(map[string]bool)
	var cleanup []string
	failure := errors.New("second registration failed")
	fail := true
	registry.Register(Manifest{
		ID: "provider", Provides: []string{"events"},
		Apply: func(_ context.Context, scope *Scope) error {
			for _, id := range []string{"tool", "listener"} {
				contributions[id] = true
				scope.OnDispose(func() {
					delete(contributions, id)
					cleanup = append(cleanup, id)
				})
			}
			if fail {
				return failure
			}
			return nil
		},
	})
	registry.Register(Manifest{ID: "dependent", Requires: []string{"events"}, Apply: func(_ context.Context, _ *Scope) error {
		if fail {
			t.Fatal("dependent applied after provider failed")
		}
		return nil
	}})
	registry.Register(Manifest{ID: "unrelated"})
	enabled := map[string]bool{"provider": true, "dependent": true, "unrelated": true}
	if err := registry.Reconcile(context.Background(), enabled); !errors.Is(err, failure) {
		t.Fatalf("Reconcile error = %v, want original apply failure", err)
	}
	if len(contributions) != 0 {
		t.Fatalf("failed apply left contributions: %v", contributions)
	}
	if !slices.Equal(cleanup, []string{"listener", "tool"}) {
		t.Fatalf("cleanup order = %v", cleanup)
	}
	if !slices.Equal(registry.ActiveIDs(), []string{"unrelated"}) {
		t.Fatalf("active plugins = %v", registry.ActiveIDs())
	}
	fail = false
	if err := registry.Reconcile(context.Background(), enabled); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !slices.Equal(registry.ActiveIDs(), []string{"dependent", "provider", "unrelated"}) {
		t.Fatalf("successful retry active plugins = %v", registry.ActiveIDs())
	}
}

func TestSetPlanOrdersOnlyChangedDependencies(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Manifest{ID: "card", Requires: []string{"events"}})
	registry.Register(Manifest{ID: "calendar", Provides: []string{"events"}, Requires: []string{"store"}})
	registry.Register(Manifest{ID: "store"})
	registry.Register(Manifest{ID: "unrelated"})
	current := map[string]bool{"store": true, "unrelated": true}
	changes, err := registry.SetPlan("card", true, current)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Change{{ID: "calendar", On: true}, {ID: "card", On: true}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("enable plan = %v, want %v", changes, want)
	}
	if len(current) != 2 {
		t.Fatal("planning mutated the stored snapshot")
	}
	current["calendar"], current["card"] = true, true
	changes, err = registry.SetPlan("store", false, current)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Change{{ID: "card"}, {ID: "calendar"}, {ID: "store"}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("disable plan = %v, want %v", changes, want)
	}
	if changes, err := registry.SetPlan("unrelated", true, current); err != nil || len(changes) != 0 {
		t.Fatalf("unchanged toggle = %v, %v", changes, err)
	}
}

func TestEssentialsCannotBeTurnedOff(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Manifest{ID: "authority", Essential: true})
	if err := registry.ValidateSet("authority", false); err == nil {
		t.Fatal("essential off accepted")
	}
	if _, err := registry.SetPlan("authority", false, map[string]bool{"authority": true}); err == nil {
		t.Fatal("essential off plan accepted")
	}
	if err := registry.Reconcile(context.Background(), map[string]bool{"authority": false}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(registry.ActiveIDs(), []string{"authority"}) {
		t.Fatal("stored false disabled an essential")
	}
	if err := registry.ValidateSet("missing", true); err == nil {
		t.Fatal("unknown plugin accepted")
	}
}

func TestEssentialDependenciesCannotBeTurnedOff(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Manifest{ID: "provider"})
	registry.Register(Manifest{ID: "essential", Essential: true, Requires: []string{"provider"}})
	if _, err := registry.SetPlan("provider", false, map[string]bool{"provider": true, "essential": true}); err == nil {
		t.Fatal("indirect essential disable accepted")
	}
	if err := registry.Reconcile(context.Background(), map[string]bool{"provider": true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Reconcile(context.Background(), map[string]bool{"provider": false}); err == nil {
		t.Fatal("essential dependency disable accepted")
	}
	if !slices.Equal(registry.ActiveIDs(), []string{"essential", "provider"}) {
		t.Fatalf("invalid reconcile changed active plugins: %v", registry.ActiveIDs())
	}
}

func TestInvalidDependencyGraphDoesNotApply(t *testing.T) {
	for name, manifests := range map[string][]Manifest{
		"missing":   {{ID: "a", Requires: []string{"missing"}}},
		"cycle":     {{ID: "a", Requires: []string{"b"}}, {ID: "b", Requires: []string{"a"}}},
		"ambiguous": {{ID: "a", Provides: []string{"service"}}, {ID: "b", Provides: []string{"service"}}},
	} {
		t.Run(name, func(t *testing.T) {
			registry := NewRegistry()
			for _, manifest := range manifests {
				manifest.Apply = func(_ context.Context, _ *Scope) error {
					t.Fatal("invalid graph started applying plugins")
					return nil
				}
				registry.Register(manifest)
			}
			if err := registry.Reconcile(context.Background(), map[string]bool{"a": true, "b": true}); err == nil {
				t.Fatal("invalid dependency graph accepted")
			}
		})
	}
}

func TestManifestSnapshotsCannotMutateRegistry(t *testing.T) {
	registry := NewRegistry()
	manifest := Manifest{ID: "a", Provides: []string{"events"}}
	registry.Register(manifest)
	manifest.Provides[0] = "changed input"
	registry.Manifests()[0].Provides[0] = "changed output"
	if got := registry.Manifests()[0].Provides[0]; got != "events" {
		t.Fatalf("mutable manifest escaped registry: %q", got)
	}
}
