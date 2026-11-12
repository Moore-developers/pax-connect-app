package controller

import (
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func validRetentionHours(hours int) bool {
	return hours == 1 || hours == 12 || hours == 24
}

func normalizedRetentionHours(hours int) int {
	if validRetentionHours(hours) {
		return hours
	}
	return logsRetentionHours
}

func (m *historyMetadata) normalizeRetention() {
	m.RetentionHours = normalizedRetentionHours(m.RetentionHours)
	m.Retention = int(logRetentionDays(m.RetentionHours))
}

// logRetentionDays rounds an hour preference up to the day count the old contract exposed.
func logRetentionDays(hours int) uint32 { return uint32((hours + 23) / 24) }

func (m historyMetadata) projectRetention(settings *pb.Settings) {
	settings.LogRetentionHours = uint32(m.RetentionHours)
	settings.LogRetentionDays = logRetentionDays(m.RetentionHours)
}
