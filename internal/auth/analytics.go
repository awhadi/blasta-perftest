package auth

import (
	"net/http"

	"github.com/awhadi/blasta-perftest/internal/analytics"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// analyticsProviders is what the settings page offers.
func analyticsProviders() []map[string]string {
	out := make([]map[string]string, len(analytics.Providers))
	for i, p := range analytics.Providers {
		out[i] = map[string]string{"id": p.ID, "name": p.Name, "idLabel": p.IDLabel, "urlHelp": p.URLHelp}
	}
	return out
}

func (s *Service) saveAnalytics(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	var in settings.Analytics
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	v, err := analytics.Clean(in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.commit(w, settings.KeyAnalytics, func() error { return s.settings.Put(settings.KeyAnalytics, v) })
}

// AnalyticsOn reports whether analytics is switched on and complete.
func (s *Service) AnalyticsOn() bool { return analytics.Ready(s.conf().Analytics) }

// AnalyticsCSP lists what the page may load and contact for the chosen analytics: nothing,
// unless an administrator switched one on.
func (s *Service) AnalyticsCSP() (script, connect, img []string) {
	return analytics.CSP(s.conf().Analytics)
}

// AnalyticsScript is the loader served at /analytics.js. For a signed-in person it is empty
// unless the administrator chose to count them too.
func (s *Service) AnalyticsScript(r *http.Request) string {
	c := s.conf().Analytics
	if !c.TrackSignedIn {
		if ck, err := r.Cookie(SessionCookie); err == nil {
			if _, err := s.Authenticate(ck.Value); err == nil {
				return ""
			}
		}
	}
	return analytics.Script(c)
}
