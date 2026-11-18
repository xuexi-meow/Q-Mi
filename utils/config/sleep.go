package config

import "fmt"

type AgentSleep struct {
	Enabled       *bool `yaml:"enabled" mapstructure:"enabled"`
	MinSeconds    int   `yaml:"min_seconds" mapstructure:"min_seconds"`
	MaxSeconds    int   `yaml:"max_seconds" mapstructure:"max_seconds"`
	WindowSeconds int   `yaml:"window_seconds" mapstructure:"window_seconds"`
	Threshold     int   `yaml:"threshold" mapstructure:"threshold"`
}

func (s AgentSleep) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

func (s AgentSleep) WithDefaults() AgentSleep {
	if s.MinSeconds == 0 {
		s.MinSeconds = 180
	}
	if s.MaxSeconds == 0 {
		s.MaxSeconds = 900
	}
	if s.WindowSeconds == 0 {
		s.WindowSeconds = 30
	}
	if s.Threshold == 0 {
		s.Threshold = 5
	}
	return s
}

func (s AgentSleep) Validate() error {
	s = s.WithDefaults()
	if s.MinSeconds < 1 || s.MaxSeconds < s.MinSeconds || s.MaxSeconds > 3600 ||
		s.WindowSeconds < 1 || s.WindowSeconds > 600 || s.Threshold < 2 || s.Threshold > 100 {
		return fmt.Errorf("agent.sleep：睡眠范围须为 1–3600 秒且上限不小于下限；唤醒窗口须为 1–600 秒，消息阈值须为 2–100")
	}
	return nil
}
