package googlesqlite_test

import (
	"fmt"
	"strings"
	"testing"
)

// The clone script dbt-virtual runs on BigQuery: clone, and on the chain-limit error make a deep copy.
const cloneWithCopyFallback = `BEGIN
  CREATE TABLE IF NOT EXISTS ` + "`p.d.%s`" + `
  CLONE ` + "`p.d.%s`" + `;
EXCEPTION WHEN ERROR THEN
  IF CONTAINS_SUBSTR(@@error.message, 'chained clones') THEN
    CREATE TABLE IF NOT EXISTS ` + "`p.d.%s`" + ` COPY ` + "`p.d.%s`" + `;
  ELSE
    RAISE USING MESSAGE = @@error.message;
  END IF;
END`

func cloneScript(target, source string) string {
	return fmt.Sprintf(cloneWithCopyFallback, target, source, target, source)
}

// TestScriptBeginBodyRuns: with no failure the BEGIN body runs and the handler does not.
func TestScriptBeginBodyRuns(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.d.t0` (id INT64)",
		"INSERT INTO `p.d.t0` (id) VALUES (1)",
		cloneScript("t1", "t0"),
	)
	if got := tableTypeOf(t, db, "d", "t1").typ; got != "CLONE" {
		t.Fatalf("t1 is %s, want CLONE", got)
	}
}

// TestScriptExceptionHandlerCopiesAtChainLimit: the 4th clone of a chain fails with BigQuery's
// message, the handler sees it in @@error.message and makes a deep copy; the copy is a BASE TABLE
// with the rows.
func TestScriptExceptionHandlerCopiesAtChainLimit(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.d.t0` (id INT64)",
		"INSERT INTO `p.d.t0` (id) VALUES (7)",
		"CREATE TABLE `p.d.t1` CLONE `p.d.t0`",
		"CREATE TABLE `p.d.t2` CLONE `p.d.t1`",
		"CREATE TABLE `p.d.t3` CLONE `p.d.t2`",
		cloneScript("t4", "t3"),
	)
	if got := tableTypeOf(t, db, "d", "t4").typ; got != "BASE TABLE" {
		t.Fatalf("t4 is %s, want BASE TABLE (a copy)", got)
	}
	var id int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM `p.d.t4`").Scan(&id); err != nil || id != 7 {
		t.Fatalf("t4 rows: id=%d err=%v, want 7", id, err)
	}
}

// TestScriptExceptionHandlerReRaises: an error the handler does not recognise is raised again with
// the same message (RAISE USING MESSAGE = @@error.message), and nothing is created.
func TestScriptExceptionHandlerReRaises(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	_, err := db.ExecContext(ctx, cloneScript("t1", "missing"))
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("got %v, want the original error about the missing table", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT * FROM `p.d.t1`"); err == nil {
		t.Fatal("t1 exists after a failed clone")
	}
}

// TestScriptRaiseUsingMessage: RAISE USING MESSAGE inside the body reaches the handler, and a handler's
// own RAISE USING MESSAGE is the error the caller gets.
func TestScriptRaiseUsingMessage(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db, "CREATE TABLE `p.d.log` (msg STRING)")
	mustExecGaps(t, ctx, db, `BEGIN
  RAISE USING MESSAGE = 'boom';
EXCEPTION WHEN ERROR THEN
  INSERT INTO `+"`p.d.log`"+` (msg) VALUES (@@error.message);
END`)
	var msg string
	if err := db.QueryRowContext(ctx, "SELECT msg FROM `p.d.log`").Scan(&msg); err != nil || msg != "boom" {
		t.Fatalf("logged %q err=%v, want boom", msg, err)
	}
	_, err := db.ExecContext(ctx, `BEGIN
  RAISE USING MESSAGE = 'first';
EXCEPTION WHEN ERROR THEN
  RAISE USING MESSAGE = CONCAT('second after ', @@error.message);
END`)
	if err == nil || !strings.Contains(err.Error(), "second after first") {
		t.Fatalf("got %v, want 'second after first'", err)
	}
}

// TestScriptBareRaiseRethrows: RAISE; in a handler raises the error being handled.
func TestScriptBareRaiseRethrows(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	_, err := db.ExecContext(ctx, `BEGIN
  RAISE USING MESSAGE = 'orig';
EXCEPTION WHEN ERROR THEN
  RAISE;
END`)
	if err == nil || !strings.Contains(err.Error(), "orig") {
		t.Fatalf("got %v, want orig", err)
	}
}

// TestScriptHandlerElseifAndElse: ELSEIF and ELSE branches are chosen by their conditions.
func TestScriptHandlerElseifAndElse(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db, "CREATE TABLE `p.d.log` (msg STRING)")
	for _, raised := range []string{"alpha", "beta", "gamma"} {
		mustExecGaps(t, ctx, db, `BEGIN
  RAISE USING MESSAGE = '`+raised+`';
EXCEPTION WHEN ERROR THEN
  IF @@error.message = 'alpha' THEN
    INSERT INTO `+"`p.d.log`"+` (msg) VALUES ('then');
  ELSEIF @@error.message = 'beta' THEN
    INSERT INTO `+"`p.d.log`"+` (msg) VALUES ('elseif');
  ELSE
    INSERT INTO `+"`p.d.log`"+` (msg) VALUES ('else');
  END IF;
END`)
	}
	rows, err := db.QueryContext(ctx, "SELECT msg FROM `p.d.log` ORDER BY rowid")
	if err != nil {
		// the emulator-style tables have no rowid alias; fall back to unordered
		rows, err = db.QueryContext(ctx, "SELECT msg FROM `p.d.log`")
		if err != nil {
			t.Fatal(err)
		}
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			t.Fatal(err)
		}
		got[m] = true
	}
	for _, want := range []string{"then", "elseif", "else"} {
		if !got[want] {
			t.Fatalf("branches taken %v, missing %s", got, want)
		}
	}
}

// TestScriptErrorVariableClearedAfterHandler: @@error.message is set only while a handler runs.
func TestScriptErrorVariableClearedAfterHandler(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db, `BEGIN
  RAISE USING MESSAGE = 'x';
EXCEPTION WHEN ERROR THEN
  SELECT 1;
END`)
	var m *string
	if err := db.QueryRowContext(ctx, "SELECT @@error.message").Scan(&m); err != nil || m != nil {
		t.Fatalf("@@error.message after the handler: %v, err=%v, want NULL", m, err)
	}
}

// TestScriptHandlerRowsAreReturned: a script run as a query returns the rows of the handler's last SELECT.
func TestScriptHandlerRowsAreReturned(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	var m string
	err := db.QueryRowContext(ctx, `BEGIN
  CREATE TABLE `+"`p.d.x`"+` CLONE `+"`p.d.nosuch`"+`;
EXCEPTION WHEN ERROR THEN
  SELECT 'handled' AS m;
END`).Scan(&m)
	if err != nil || m != "handled" {
		t.Fatalf("got %q err=%v, want handled", m, err)
	}
}

// TestScriptBlockWithoutHandlerStillFlattens: a BEGIN ... END without EXCEPTION behaves as before.
func TestScriptBlockWithoutHandlerStillFlattens(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db, `BEGIN
  CREATE TABLE `+"`p.d.a`"+` (id INT64);
  INSERT INTO `+"`p.d.a`"+` (id) VALUES (3);
END`)
	var id int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM `p.d.a`").Scan(&id); err != nil || id != 3 {
		t.Fatalf("id=%d err=%v, want 3", id, err)
	}
}
