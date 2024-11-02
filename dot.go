package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

func testDotAlgorithm(state *State) {
	var dotAction string
	if len(os.Args) > 2 {
		dotAction = os.Args[2]
	}

	switch dotAction {
	case "test":
		dotTest(state)
	case "backfill":
		dotBackfill(state)
	case "validate-timestamps":
		dotValidateTimestamps(state)
	default:
		slog.Error("Invalid action")
	}
}

func assertGoodDotTimestamp(t time.Time) {
	if t.Second() != 0 {
		panic("Non zero second in time")
	}
	if t.Nanosecond() != 0 {
		panic("Non zero nanosecond in time")
	}
}
func assertGoodDotDelta(lastT, incomingT time.Time) {
	dur := incomingT.Sub(lastT)

	if dur.Seconds() != 60 {
		slog.Error("Non zero second in time delta...", slog.Float64("delta", dur.Seconds()))
		panic("Non zero second in time delta...")
	}
}

func dotTest(state *State) {
	now := time.Now()

	startAll := now.Add(-24 * time.Hour)
	startAll = startAll.Add(1 * time.Hour).Add(-1 * time.Duration(startAll.Second()) * time.Second).Add(-1 * time.Duration(startAll.Nanosecond()) * time.Nanosecond)
	//startAll := now.Add(-1 * time.Hour)
	endAll := now

	dotState := NewDotV1()
	dotValues := make([]Dot, 0)
	for t := startAll; t.After(endAll) == false; t = t.Add(dotState.TimePeriod()) {
		startT := t
		endT := t.Add(dotState.TimePeriod())
		assertGoodDotTimestamp(startT)
		assertGoodDotTimestamp(endT)
		rows, err := state.db.Query(`SELECT post_hash FROM sentiment_events WHERE timestamp > ? and timestamp < ? and sentiment_analyst = ?`,
			startT.UnixMilli(), endT.UnixMilli(), "v3")
		if err != nil {
			panic(err)
		}
		sentiments := make([]string, 0)
		for rows.Next() {
			var postHash string
			err := rows.Scan(&postHash)
			if err != nil {
				panic(err)
			}

			// for each event, get its sentiment (this should be a join maybe)
			row := state.db.QueryRow(`SELECT sentiment_data FROM sentiment_data WHERE post_hash=? AND sentiment_analyst=?`, postHash, state.cfg.embeddingVersion)
			var sentimentData string
			err = row.Scan(&sentimentData)
			if err != nil {
				slog.Error("no sentiment data found in sentiment_data table", slog.String("postHash", postHash))
				continue
			}
			sentiments = append(sentiments, sentimentData)
		}

		if len(sentiments) > 0 {
			dotState.Forward(sentiments)
			dotValues = append(dotValues, Dot{UnixTimestamp: startT.Unix(), Value: dotState.d})
			fmt.Println(t, dotState.d)
		} else {
			dotValues = append(dotValues, Dot{UnixTimestamp: startT.Unix(), Value: dotState.d})
			fmt.Println(t, "no sentiments")
		}
	}
	fmt.Println(dotState.d)
	fname, err := GenerateDotPlot(dotValues)
	if err != nil {
		panic(err)
	}
	fmt.Println(fname)

	// Create the command
	cmd := exec.Command("feh", fname)

	// Start the process
	if err := cmd.Start(); err != nil {
		panic(err)
	}

	if err := cmd.Wait(); err != nil {
		panic(err)
	}

}

func dotBackfill(state *State) {
	now := time.Now()

	row := state.db.QueryRow(`SELECT timestamp FROM sentiment_events WHERE sentiment_analyst = ? ORDER BY timestamp ASC`, "v3")
	var minTimestamp int64
	err := row.Scan(&minTimestamp)
	if errors.Is(err, sql.ErrNoRows) {
		minTimestamp = time.Now().UnixMilli()
	} else if err != nil {
		panic(err)
	}

	startAll := time.UnixMilli(minTimestamp)
	// always start at the next available hour, on 00 seconds too
	startAll = startAll.Add(1 * time.Hour).Add(-time.Duration(startAll.Minute()) * time.Minute).Add(-1 * time.Duration(startAll.Second()) * time.Second).Add(-1 * time.Duration(startAll.Nanosecond()) * time.Nanosecond)

	// keep walking TimePeriod() steps until we get to a timestamp that is within the last 30 minutes
	// (the dot processor is the one that will do final backfilling of the last N amounts of time. this process is just to ease it up)
	dotState := NewDotV1()
	endAll := now.Add(-30 * time.Minute)

	slog.Info("backfilling", slog.String("now", now.String()), slog.String("startAll", startAll.String()), slog.String("endAll", endAll.String()))
	for t := startAll; t.After(endAll) == false; t = t.Add(dotState.TimePeriod()) {
		startT := t
		endT := t.Add(dotState.TimePeriod())
		assertGoodDotTimestamp(startT)
		assertGoodDotTimestamp(endT)

		row := state.db.QueryRow(`SELECT data FROM dot_data WHERE timestamp = ? AND dot_analyst = ?`, startT.Unix(), "v1")
		var maybeData string
		err := row.Scan(&maybeData)
		if err == nil {
			slog.Info("dot data already exists here, skipping", slog.Int64("timestamp", startT.Unix()))
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			panic(err)
		}

		rows, err := state.db.Query(`SELECT post_hash FROM sentiment_events WHERE timestamp > ? and timestamp < ? and sentiment_analyst = ?`,
			startT.UnixMilli(), endT.UnixMilli(), "v3")
		if err != nil {
			panic(err)
		}
		sentiments := make([]string, 0)
		for rows.Next() {
			var postHash string
			err := rows.Scan(&postHash)
			if err != nil {
				panic(err)
			}

			// for each event, get its sentiment (this should be a join maybe)
			row := state.db.QueryRow(`SELECT sentiment_data FROM sentiment_data WHERE post_hash=? AND sentiment_analyst=?`, postHash, state.cfg.embeddingVersion)
			var sentimentData string
			err = row.Scan(&sentimentData)
			if err != nil {
				slog.Error("no sentiment data found in sentiment_data table", slog.String("postHash", postHash))
				continue
			}
			sentiments = append(sentiments, sentimentData)
		}

		currentDotValue := dotState.d
		if len(sentiments) > 0 {
			dotState.Forward(sentiments)
			currentDotValue = dotState.d
		}

		wrapped := map[string]any{
			"d": currentDotValue,
		}

		encoded, err := json.Marshal(wrapped)
		if err != nil {
			panic(err)
		}

		slog.Info("backfilling...", slog.Int64("timestamp", startT.Unix()), slog.Float64("value", currentDotValue))
		_, err = state.db.Exec(`INSERT INTO dot_data (timestamp, dot_analyst, data) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, startT.Unix(), "v1", string(encoded))
		if err != nil {
			panic(err)
		}

	}
	slog.Info("dot backfill complete")
}

func lastDot(state *State) (time.Time, float64, bool) {
	row := state.db.QueryRow(`SELECT timestamp, data FROM dot_data WHERE dot_analyst = ? ORDER BY timestamp DESC LIMIT 1`, "v1")
	var maxTimestamp int64
	var dotDataEncoded string
	err := row.Scan(&maxTimestamp, &dotDataEncoded)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, 0, false
	}
	if err != nil {
		panic(err)
	}

	var dotData map[string]any
	err = json.Unmarshal([]byte(dotDataEncoded), &dotData)
	if err != nil {
		panic(err)
	}

	dd, ok := dotData["d"].(float64)
	if !ok {
		slog.Error("invalid dot data", slog.String("data", dotDataEncoded))
		panic("invalid dot value")
	}
	return time.Unix(maxTimestamp, 0), dd, true
}

func maxEventTimestamp(state *State) time.Time {
	row := state.db.QueryRow(`SELECT max(timestamp) FROM sentiment_events WHERE sentiment_analyst = ?`, "v3")
	var maxTimestamp int64
	err := row.Scan(&maxTimestamp)
	if err != nil {
		panic(err)
	}
	return time.UnixMilli(maxTimestamp)
}

func minEventTimestamp(state *State) (time.Time, bool) {
	row := state.db.QueryRow(`SELECT timestamp FROM sentiment_events WHERE sentiment_analyst = ? ORDER BY timestamp ASC`, "v3")
	var minTimestamp int64
	err := row.Scan(&minTimestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false
	}
	if err != nil {
		panic(err)
	}
	return time.UnixMilli(minTimestamp), true
}

func dotProcessor(state *State) {
	// first we need to play catch up since the webapp might've restarted!!

	now := time.Now()
	startAll, _, _ := lastDot(state)
	// if it's been over 30 minutes, we need to backfill until the best timestamp, then backfill ourselves minute by minute
	delta := now.Sub(startAll).Seconds()
	slog.Info("do we need to backfill?", slog.Float64("delta", delta), slog.Float64("target", time.Duration(30*time.Minute).Seconds()))
	if delta > time.Duration(30*time.Minute).Seconds() {
		slog.Info("backfilling!")
		dotBackfill(state)
	} else {
		slog.Info("not backfilling")
	}

	dot := NewDotV1()
	ticker := time.Tick(dot.TimePeriod())

	// every minute, we must check which chunks of posts we can process now
	slog.Info("entering dot worker loop..")
	for {
		select {
		case <-ticker:
			// find the chunks by querying maxTimestamp after backfill
			// and ticking forward TimePeriod steps until we find the maxTimestamp of sentiment_events

			lastDotTimestamp, lastDotValue, ok := lastDot(state)
			eventTimestamp := maxEventTimestamp(state)
			if !ok {
				panic("no dot data, please run the backfill task first")
			}
			assertGoodDotTimestamp(lastDotTimestamp)

			lastDotState := DotV1{d: lastDotValue}

			lastProcessedTimestamp := lastDotTimestamp

			for t := lastDotTimestamp.Add(dot.TimePeriod()); t.Before(eventTimestamp); t = t.Add(dot.TimePeriod()) {
				startT := t
				assertGoodDotDelta(lastProcessedTimestamp, startT)
				endT := t.Add(dot.TimePeriod())
				if endT.After(eventTimestamp) {
					break
				}
				assertGoodDotTimestamp(startT)
				assertGoodDotTimestamp(endT)

				// we're in a chunk [startT, endT], compute sentiments and set dot value on startT!
				slog.Info("computing sentiments..")
				rows, err := state.db.Query(`SELECT post_hash FROM sentiment_events WHERE timestamp >= ? and timestamp <= ? and sentiment_analyst = ?`,
					startT.UnixMilli(), endT.UnixMilli(), state.cfg.embeddingVersion)
				if err != nil {
					panic(err)
				}
				sentiments := make([]string, 0)
				for rows.Next() {
					var postHash string
					err := rows.Scan(&postHash)
					if err != nil {
						panic(err)
					}

					// for each event, get its sentiment (this should be a join maybe)
					row := state.db.QueryRow(`SELECT sentiment_data FROM sentiment_data WHERE post_hash=? AND sentiment_analyst=?`, postHash, state.cfg.embeddingVersion)
					var sentimentData string
					err = row.Scan(&sentimentData)
					if err != nil {
						slog.Error("no sentiment data found in sentiment_data table", slog.String("postHash", postHash))
						continue
					}
					sentiments = append(sentiments, sentimentData)
				}

				currentDotValue := lastDotState.d
				if len(sentiments) > 0 {
					lastDotState.Forward(sentiments)
					currentDotValue = lastDotState.d
				} else {
					slog.Error("no sentiments found!!!!! problem!!! (workers died or not running fast enough)")
					state.PrintState()
				}

				wrapped := map[string]any{
					"d": currentDotValue,
				}

				encoded, err := json.Marshal(wrapped)
				if err != nil {
					panic(err)
				}

				slog.Info("dot!", slog.Int64("timestamp", startT.Unix()), slog.Float64("value", currentDotValue))
				_, err = state.db.Exec(`INSERT INTO dot_data (timestamp, dot_analyst, data) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, startT.Unix(), "v1", string(encoded))
				if err != nil {
					panic(err)
				}
				lastProcessedTimestamp = startT
			}
		}
	}
}

func dotValidateTimestamps(state *State) {
	rows, err := state.db.Query(`SELECT timestamp FROM dot_data WHERE dot_analyst = ? ORDER BY timestamp ASC`, "v1")
	if err != nil {
		panic(err)
	}

	var lastTimestamp int64
	for rows.Next() {
		var timestamp int64
		err := rows.Scan(&timestamp)
		if err != nil {
			panic(err)
		}
		t := time.Unix(timestamp, 0)
		slog.Info("validating", slog.Int64("timestamp", timestamp))
		assertGoodDotTimestamp(t)

		delta := timestamp - lastTimestamp
		if lastTimestamp != 0 {
			if delta != 60 {
				panic(fmt.Sprintf("bad timestamp %d, delta %d", timestamp, delta))
			}
		}
		lastTimestamp = timestamp

		slog.Info("validated", slog.Int64("timestamp", timestamp), slog.Int64("delta", delta))
	}

	slog.Info("timestamp validation complete")
}
