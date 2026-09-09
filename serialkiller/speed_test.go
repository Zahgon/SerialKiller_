package serialkiller

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"
)

// Port of org.nibblesec.tools.SerialKillerSpeedTest.speedTest.
//
// The Java test serializes a util.Person(1, "Test"), then deserializes it
// 10.000 times with a plain ObjectInputStream and 10.000 times with a
// SerialKiller (using serialkiller-speedtest.conf, whose whitelist is ".*"),
// printing the elapsed time for each. It is a timing/observability test rather
// than an assertion test: its purpose is to verify SerialKiller can process a
// whitelisted object graph repeatedly without error and to report the overhead.
func TestSerialKiller_Speed(t *testing.T) {
	ResetConfigCache()

	data, err := os.ReadFile("../testdata/person.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	const iterations = 10000

	// Baseline: walk the stream without filtering ("WITHOUT SerialKiller").
	// Warm-up.
	for i := 0; i < 1000; i++ {
		or := newObjectReader(bytes.NewReader(data), nil)
		if _, err := or.ReadStream(); err != nil {
			t.Fatalf("baseline warm-up failed: %v", err)
		}
	}
	startPlain := time.Now()
	for i := 0; i < iterations; i++ {
		or := newObjectReader(bytes.NewReader(data), nil)
		if _, err := or.ReadStream(); err != nil {
			t.Fatalf("baseline read failed at %d: %v", i, err)
		}
	}
	elapsedPlain := time.Since(startPlain).Milliseconds()
	fmt.Printf("Result (WITHOUT SerialKiller): %dms for 10.000 iterations\n", elapsedPlain)

	// With SerialKiller and the whitelist ".*" config.
	// Warm-up.
	for i := 0; i < 1000; i++ {
		sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller-speedtest.conf")
		if err != nil {
			t.Fatalf("failed to construct SerialKiller: %v", err)
		}
		sk.SetLogger(silentLogger{})
		if _, err := sk.ReadObject(); err != nil {
			t.Fatalf("SerialKiller warm-up failed: %v", err)
		}
	}
	startSK := time.Now()
	for i := 0; i < iterations; i++ {
		sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller-speedtest.conf")
		if err != nil {
			t.Fatalf("failed to construct SerialKiller: %v", err)
		}
		sk.SetLogger(silentLogger{})
		if _, err := sk.ReadObject(); err != nil {
			t.Fatalf("SerialKiller read failed at %d: %v", i, err)
		}
	}
	elapsedSK := time.Since(startSK).Milliseconds()
	fmt.Printf("Result (WITH SerialKiller): %dms for 10.000 iterations\n", elapsedSK)
}
