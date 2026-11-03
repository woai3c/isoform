package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Private podcast feed (docs/notifications-design.md section 7). The feed lists
// the spoken briefs of the last 14 days as an RSS 2.0 podcast so a podcast app
// can follow it. In this pass it is served from the Mac only, on the loopback
// interface under a random secret path, because a podcast app cannot decrypt
// and a relay-hosted feed would hold plaintext audio. The relay-hosted version
// is designed in the document and not built.

const (
	mobileBriefFeedPort       = 47831
	mobileBriefFeedSecretFile = "feed-secret"
	mobileBriefFeedItems      = 14
	mobileBriefFeedTitle      = "Isoform brief"
	mobileBriefFeedSecretLen  = 16
	mobileBriefFeedReadWait   = 10 * time.Second
	mobileBriefFeedWriteWait  = 60 * time.Second
)

var errMobileBriefFeedPort = errors.New("the podcast feed could not start")

// feedBriefs applies the same current source policy as phone and Today reads.
// Stored audio is a synthesis of the original inputs, so losing any input
// removes its recording as well as its narrative from the feed projection.
func (s *mobileBriefService) feedBriefs() []MobileBriefV1 {
	if s.app == nil {
		return nil
	}
	briefs := s.store.briefs()
	for i := range briefs {
		briefs[i] = s.app.mobileBriefForDelivery(briefs[i])
	}
	return briefs
}

// mobileBriefFeedItem is one spoken brief in the feed.
type mobileBriefFeedItem struct {
	BriefID         string
	Slot            string
	LocalDate       string
	AsOf            time.Time
	Mime            string
	Bytes           int
	DurationSeconds float64
	Summary         string
}

// mobileBriefFeedItemsFrom lists the stored briefs that have audio, newest
// first, at most mobileBriefFeedItems.
func mobileBriefFeedItemsFrom(briefs []MobileBriefV1) []mobileBriefFeedItem {
	var out []mobileBriefFeedItem
	for _, brief := range briefs {
		if brief.Audio == nil || brief.Audio.Bytes == 0 {
			continue
		}
		item := mobileBriefFeedItem{
			BriefID: brief.BriefID, Slot: mobileBriefDisplaySlot(brief, defaultMobileBriefSettings()), LocalDate: brief.LocalDate, AsOf: mobileBriefAsOfTime(brief),
			Mime: brief.Audio.Mime, Bytes: brief.Audio.Bytes, DurationSeconds: brief.Audio.DurationSeconds,
		}
		if len(brief.Narrative) > 0 {
			item.Summary = strings.TrimSpace(brief.Narrative[0].Text)
		}
		out = append(out, item)
		if len(out) >= mobileBriefFeedItems {
			break
		}
	}
	return out
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Itunes  string     `xml:"xmlns:itunes,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Language    string    `xml:"language"`
	Author      string    `xml:"itunes:author"`
	Block       string    `xml:"itunes:block"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string       `xml:"title"`
	GUID        rssGUID      `xml:"guid"`
	PubDate     string       `xml:"pubDate"`
	Description string       `xml:"description,omitempty"`
	Enclosure   rssEnclosure `xml:"enclosure"`
	Duration    string       `xml:"itunes:duration,omitempty"`
}

type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// mobileBriefFeedXML renders the podcast feed. baseURL is the secret feed root
// (no trailing slash); each item's enclosure sits under <baseURL>/audio/.
func mobileBriefFeedXML(items []mobileBriefFeedItem, hubName, baseURL string) ([]byte, error) {
	channel := rssChannel{
		Title:       mobileBriefFeedTitle,
		Link:        baseURL + "/brief.xml",
		Description: fmt.Sprintf("Your spoken briefs from %s. Private: this feed lives on your computer.", strings.TrimSpace(hubName)),
		Language:    "en",
		Author:      "Isoform",
		Block:       "Yes",
		Items:       []rssItem{},
	}
	for _, item := range items {
		ext := mobileBriefAudioExtWAV
		if item.Mime == mobileBriefAudioMimeM4A {
			ext = mobileBriefAudioExtM4A
		}
		title := mobileBriefFeedItemTitle(item)
		channel.Items = append(channel.Items, rssItem{
			Title:       title,
			GUID:        rssGUID{IsPermaLink: "false", Value: item.BriefID},
			PubDate:     item.AsOf.UTC().Format(time.RFC1123Z),
			Description: item.Summary,
			Enclosure:   rssEnclosure{URL: baseURL + "/audio/" + item.BriefID + ext, Length: strconv.Itoa(item.Bytes), Type: item.Mime},
			Duration:    mobileBriefFeedDuration(item.DurationSeconds),
		})
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")
	if err := encoder.Encode(rssFeed{Version: "2.0", Itunes: "http://www.itunes.com/dtds/podcast-1.0.dtd", Channel: channel}); err != nil {
		return nil, err
	}
	buf.WriteString("\n")
	return buf.Bytes(), nil
}

// mobileBriefFeedItemTitle names an episode by its slot and local date.
func mobileBriefFeedItemTitle(item mobileBriefFeedItem) string {
	slot := ""
	switch mobileBriefDisplaySlot(MobileBriefV1{Slot: item.Slot, AsOf: formatMobileBriefTime(item.AsOf), Timezone: "UTC"}, defaultMobileBriefSettings()) {
	case mobileBriefSlotMorning:
		slot = "Morning brief"
	case mobileBriefSlotEvening:
		slot = "Evening brief"
	default:
	}
	if day, err := time.Parse("2006-01-02", item.LocalDate); err == nil {
		return slot + ", " + day.Format("Monday, January 2")
	}
	return slot
}

// mobileBriefFeedDuration renders seconds as H:MM:SS for itunes:duration.
func mobileBriefFeedDuration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	total := int(seconds + 0.5)
	return fmt.Sprintf("%d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

// mobileBriefFeedServer serves the feed on the loopback interface under a
// secret path. It is started when the schedule's feed switch is on.
type mobileBriefFeedServer struct {
	stateDir string
	hubName  func() string
	// briefs must project stored documents through the current delivery policy.
	briefs       func() []MobileBriefV1
	audioDir     string
	audioAllowed func(string) bool

	mu       sync.Mutex
	secret   string
	listener net.Listener
	server   *http.Server
}

func newMobileBriefFeedServer(stateDir, audioDir string, hubName func() string, briefs func() []MobileBriefV1) *mobileBriefFeedServer {
	return &mobileBriefFeedServer{stateDir: stateDir, audioDir: audioDir, hubName: hubName, briefs: briefs}
}

// loadSecret reads or creates the random secret that names the feed path.
func (f *mobileBriefFeedServer) loadSecret() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secret != "" {
		return f.secret, nil
	}
	if f.stateDir == "" {
		return "", errMobileBriefStateUnavailable
	}
	path := filepath.Join(f.stateDir, mobileBriefFeedSecretFile)
	if data, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == mobileBriefFeedSecretLen*2 {
			if _, err := hex.DecodeString(secret); err == nil {
				f.secret = secret
				return secret, nil
			}
		}
	}
	raw := make([]byte, mobileBriefFeedSecretLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(raw)
	if err := os.MkdirAll(f.stateDir, 0700); err != nil {
		return "", err
	}
	if err := writeMobileBriefFileAtomic(f.stateDir, mobileBriefFeedSecretFile, []byte(secret+"\n")); err != nil {
		return "", err
	}
	f.secret = secret
	return secret, nil
}

// Start listens on the feed port and returns the feed URL. A taken port is an
// error, not a fallback: the address is what the podcast app subscribed to,
// and a random port would break the subscription on the next launch.
// Starting an already running server returns its URL.
func (f *mobileBriefFeedServer) Start() (string, error) {
	secret, err := f.loadSecret()
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener != nil {
		return f.urlLocked(), nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(mobileBriefFeedPort)))
	if err != nil {
		return "", fmt.Errorf("%w: port %d on this computer is in use", errMobileBriefFeedPort, mobileBriefFeedPort)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/feed/", f.serve)
	f.listener = listener
	f.server = &http.Server{Handler: mux, ReadHeaderTimeout: mobileBriefFeedReadWait, WriteTimeout: mobileBriefFeedWriteWait}
	go func(server *http.Server, listener net.Listener) {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Printf("[mobile-brief] the feed server stopped: %v", err)
		}
	}(f.server, listener)
	_ = secret
	return f.urlLocked(), nil
}

// Stop closes the listener. The secret stays, so the URL survives a restart.
func (f *mobileBriefFeedServer) Stop() {
	f.mu.Lock()
	server := f.server
	// Serve may not have registered this listener with http.Server yet.
	// Release the port before a concurrent Start can try to bind it again.
	if f.listener != nil {
		_ = f.listener.Close()
	}
	f.server, f.listener = nil, nil
	f.mu.Unlock()
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// URL returns the feed URL while the server runs, or "".
func (f *mobileBriefFeedServer) URL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener == nil {
		return ""
	}
	return f.urlLocked()
}

func (f *mobileBriefFeedServer) urlLocked() string {
	return "http://" + f.listener.Addr().String() + "/feed/" + f.secret + "/brief.xml"
}

func (f *mobileBriefFeedServer) baseURLLocked() string {
	return "http://" + f.listener.Addr().String() + "/feed/" + f.secret
}

// serve answers /feed/<secret>/brief.xml and /feed/<secret>/audio/<briefId>.<ext>.
// Anything else, including a wrong secret, is a plain 404.
func (f *mobileBriefFeedServer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/feed/")
	secret, path, _ := strings.Cut(rest, "/")
	f.mu.Lock()
	known := f.secret
	base := ""
	if f.listener != nil {
		base = f.baseURLLocked()
	}
	f.mu.Unlock()
	if known == "" || base == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(known)) != 1 {
		http.NotFound(w, r)
		return
	}
	var briefs []MobileBriefV1
	if f.briefs != nil {
		briefs = f.briefs()
	}
	switch path {
	case "brief.xml":
		hubName := mobileBriefDefaultHub
		if f.hubName != nil {
			if name := strings.TrimSpace(f.hubName()); name != "" {
				hubName = name
			}
		}
		body, err := mobileBriefFeedXML(mobileBriefFeedItemsFrom(briefs), hubName, base)
		if err != nil {
			http.Error(w, "feed unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	default:
		if !strings.HasPrefix(path, "audio/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path, "audio/")
		ext := filepath.Ext(name)
		briefID := strings.TrimSuffix(name, ext)
		// A podcast client can retain the enclosure URL after the feed hides
		// an episode. The file's existence is not permission to deliver it.
		allowed := false
		for _, brief := range briefs {
			if brief.BriefID == briefID && brief.Audio != nil && brief.Audio.Bytes > 0 {
				allowed = true
				break
			}
		}
		if !allowed || (f.audioAllowed != nil && !f.audioAllowed(briefID)) {
			http.NotFound(w, r)
			return
		}
		audioPath, mime, ok := mobileBriefAudioPath(f.audioDir, briefID)
		if !ok || filepath.Ext(audioPath) != ext {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		http.ServeFile(w, r, audioPath)
	}
}
