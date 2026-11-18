package config

import "testing"

func TestSleepDefaultsAndRejectsUnboundedSettings(t *testing.T) {
	defaults := (AgentSleep{}).WithDefaults()
	if defaults.MinSeconds != 180 || defaults.MaxSeconds != 900 || defaults.WindowSeconds != 30 || defaults.Threshold != 5 {
		t.Fatalf("defaults = %+v", defaults)
	}
	for _, settings := range []AgentSleep{
		{MinSeconds: -1}, {MinSeconds: 901, MaxSeconds: 900},
		{MaxSeconds: 3601}, {WindowSeconds: -1}, {WindowSeconds: 601},
		{Threshold: 1}, {Threshold: 101},
	} {
		if err := settings.Validate(); err == nil {
			t.Fatalf("accepted unsafe sleep settings: %+v", settings)
		}
	}
}
