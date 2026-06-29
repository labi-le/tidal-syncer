package config

func (w DaemonTimeWindow) DelayRange() DurationRange {
	return DurationRange{Min: w.Min, Max: w.Max}
}
