package config

import "testing"

func diffBase() *Config {
	return &Config{
		Bot:        &Bot{QQ_Number: 1000, Admin_Number: []int{1}},
		NetSetting: &NetSetting{Host: "0.0.0.0", Port: 9090, Pattern: "/Q-Mi", Token: "t"},
		Napcat:     &Napcat{HTTP: NapcatConn{Enable: true, Host: "127.0.0.1", Port: 8080}},
		Storage:    &Storage{Enable: true, Path: "Q-Mi.db"},
		WebUI:      &WebUIConfig{Enable: true, Host: "127.0.0.1", Port: 8848, Token: "tok"},
		Agent: &AgentConfig{
			Enable: true, BaseURL: "http://a", APIKey: "k", Model: "m",
			Temperature: 0.8, MaxToolRounds: 5, Debounce: 3,
			History: AgentHistory{Max: 40, Trim: 20},
		},
	}
}

func TestDiffReportsHotFieldOnly(t *testing.T) {
	base := diffBase()
	next := diffBase()
	next.Agent.Temperature = 0.3
	next.Agent.MaxToolRounds = 9

	report := Diff(base, next)
	if !report.Changed() {
		t.Fatal("expected changes")
	}
	if report.RestartRequired() {
		t.Fatalf("hot-only change must not require restart: %v", report.Fields(LevelRestart))
	}
	hot := report.Fields(LevelHot)
	if len(hot) != 2 || hot[0] != "agent.temperature" || hot[1] != "agent.max_tool_rounds" {
		t.Fatalf("hot fields = %v, want [agent.temperature agent.max_tool_rounds]", hot)
	}
}

func TestDiffReportsRestartField(t *testing.T) {
	base := diffBase()
	next := diffBase()
	next.NetSetting.Port = 9091

	report := Diff(base, next)
	if !report.RestartRequired() {
		t.Fatal("netsetting change must require restart")
	}
	if got := report.Fields(LevelRestart); len(got) != 1 || got[0] != "netsetting" {
		t.Fatalf("restart fields = %v, want [netsetting]", got)
	}
	if len(report.Fields(LevelHot)) != 0 {
		t.Fatalf("unexpected hot fields: %v", report.Fields(LevelHot))
	}
}

func TestDiffAdminListIsHot(t *testing.T) {
	base := diffBase()
	next := diffBase()
	next.Bot.Admin_Number = []int{1, 2}

	report := Diff(base, next)
	if report.RestartRequired() {
		t.Fatal("admin_number should apply without restart")
	}
	if got := report.Fields(LevelHot); len(got) != 1 || got[0] != "bot.admin_number" {
		t.Fatalf("hot fields = %v, want [bot.admin_number]", got)
	}
}

func TestDiffNoChange(t *testing.T) {
	base := diffBase()
	next := diffBase()
	if report := Diff(base, next); report.Changed() {
		t.Fatalf("identical configs must report no change: %s", report.Summary())
	}
	if report := Diff(nil, next); report.Changed() {
		t.Fatal("nil side must report no change")
	}
}

func TestDiffSummaryIsStable(t *testing.T) {
	base := diffBase()
	next := diffBase()
	next.Agent.Temperature = 0.1
	next.NetSetting.Host = "127.0.0.1"
	got := Diff(base, next).Summary()
	want := "netsetting(restart), agent.temperature(hot)"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}
