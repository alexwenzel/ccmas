package domain

import "testing"

// Die vorläufigen Zustandslisten (Reservierung, Zustellung, Outbox) sind zugleich die CHECK-Listen der Migration 001.
// Hier wird festgehalten, was sie ausmacht: eindeutige, gültige Werte, und ein fremder Wert ist ungültig.
func TestPendingStateListsAreWellFormed(t *testing.T) {
	check := func(name string, all []string, valid func(string) bool, want int) {
		t.Helper()
		if len(all) != want {
			t.Errorf("%s: %d Zustände, erwartet %d", name, len(all), want)
		}
		seen := map[string]bool{}
		for _, s := range all {
			if s == "" || seen[s] || !valid(s) {
				t.Errorf("%s: Zustand %q leer, doppelt oder ungültig", name, s)
			}
			seen[s] = true
		}
		for _, bad := range []string{"", "Active", "active ", "unknown"} {
			if seen[bad] {
				continue
			}
			if valid(bad) {
				t.Errorf("%s: %q gilt als gültig", name, bad)
			}
		}
	}
	var leases, deliveries, outbox []string
	for _, s := range LeaseStates() {
		leases = append(leases, string(s))
	}
	for _, s := range DeliveryStates() {
		deliveries = append(deliveries, string(s))
	}
	for _, s := range OutboxStates() {
		outbox = append(outbox, string(s))
	}
	check("LeaseState", leases, func(s string) bool { return LeaseState(s).Valid() }, 4)
	check("DeliveryState", deliveries, func(s string) bool { return DeliveryState(s).Valid() }, 3)
	check("OutboxState", outbox, func(s string) bool { return OutboxState(s).Valid() }, 7)
}
