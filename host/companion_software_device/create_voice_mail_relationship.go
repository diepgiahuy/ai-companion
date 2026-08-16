package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	var databaseURL string
	var deviceA string
	var deviceB string
	flag.StringVar(&databaseURL, "database-url", "", "PostgreSQL URL")
	flag.StringVar(&deviceA, "device-a", "", "first enrolled device")
	flag.StringVar(&deviceB, "device-b", "", "second enrolled device")
	flag.Parse()

	databaseURL = strings.TrimSpace(databaseURL)
	deviceA = strings.TrimSpace(deviceA)
	deviceB = strings.TrimSpace(deviceB)
	if databaseURL == "" || deviceA == "" || deviceB == "" || deviceA == deviceB {
		fmt.Fprintln(os.Stderr, "database-url and two distinct device ids are required")
		os.Exit(2)
	}

	// Both Tier-1 devices are deliberately enrolled under the same synthetic
	// owner. Canonical device ordering matches the production relationship
	// constraint; the fixture only establishes authoritative precondition state.
	if deviceB < deviceA {
		deviceA, deviceB = deviceB, deviceA
	}
	relationshipID := "tier1-rel-" + deviceA + "-" + deviceB

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect PostgreSQL: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	var returned string
	err = conn.QueryRow(ctx, `
		INSERT INTO device_relationships(
			relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at
		) VALUES($1,$2,$3,'default','default',now())
		ON CONFLICT(device_a_id,device_b_id) WHERE revoked_at IS NULL
		DO UPDATE SET user_a_id=EXCLUDED.user_a_id, user_b_id=EXCLUDED.user_b_id
		RETURNING relationship_id`, relationshipID, deviceA, deviceB).Scan(&returned)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create Tier-1 relationship: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(returned)
}
