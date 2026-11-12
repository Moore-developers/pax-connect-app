package config

// MigrateExit separates legacy exit facts without fabricating a new sample time.
// It is idempotent and never replaces an independently saved exit result.
func (d *MeasurementDocument) MigrateExit() {
	d.SchemaVersion = 2
	for id, record := range d.Records {
		old := record.Latency
		if record.Exit == nil && old != nil && (old.ExitIP != "" || old.ExitError != "" || old.LastExitLocation != nil) {
			value := &SavedMeasurement{At: old.At, ConfigRevision: old.ConfigRevision, ExitIP: old.ExitIP, ExitLocation: old.ExitLocation, LastExitLocation: old.LastExitLocation, Success: old.ExitIP != "", Error: old.ExitError}
			if old.ExitLocation != nil && !old.ExitLocation.At.IsZero() {
				value.At = old.ExitLocation.At
			}
			record.Exit = value
		}
		if old != nil {
			clean := *old
			clean.ExitIP = ""
			clean.ExitError = ""
			clean.ExitLocation = nil
			clean.LastExitLocation = nil
			record.Latency = &clean
		}
		d.Records[id] = record
	}
}
