// Package processidentity reads PID, UID and kernel birth time on Darwin.
// The user daemon and root core use it to detect death and PID reuse. It does not launch,
// signal, persist or otherwise manage processes or product sessions.
package processidentity
