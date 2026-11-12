// Package network provides read-only physical-path, routing-ownership and
// traffic-path observations plus physically bound measurement clients.
// It does not create TUNs, mutate proxies/routes/DNS, restore global snapshots
// or stop external processes. Root core owns product network lifecycle;
// controller consumes standard observation values, never upstream types.
package network
