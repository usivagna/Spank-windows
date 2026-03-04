// Package sensor provides an accelerometer reader interface and
// a Windows implementation using the Windows Sensor API (COM).
package sensor

import "time"

// Sample holds a 3-axis accelerometer reading in g.
type Sample struct {
	X, Y, Z   float64
	Timestamp time.Time
}
