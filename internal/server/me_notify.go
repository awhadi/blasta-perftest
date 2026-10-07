package server

// exportNotifications is how a person wants to be told about their tests, for their data download.
// The webhook addresses are secrets (whoever has one can post to that channel), so only the fact
// that one is set is included.
func (a *API) exportNotifications(owner string) map[string]any {
	d := a.mgr.DB()
	if d == nil || a.auth == nil {
		return map[string]any{}
	}
	p, err := a.loadPrefs(d, owner)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"email": p.Email, "on": p.On, "slackSet": p.Slack != "", "teamsSet": p.Teams != "", "webhookSet": p.Webhook != ""}
}
