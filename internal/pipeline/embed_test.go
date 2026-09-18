package pipeline

import (
	"strings"
	"testing"
)

func TestCommonScriptEmbedded(t *testing.T) {
	s := CommonScript()
	if s == "" {
		t.Fatal("common.sh is empty")
	}
	for _, fn := range []string{"log()", "wait_for()", "hold_until_job_done()", "mark_failed()", "pipeline_init()", "log_file_enabled()", "prune_pipeline_logs()", "already_done_hold()", "already_done_exit()", "clear_step_failed()", "log_file_lines()", "dump_heartbeat_start()", "dump_heartbeat_stop()"} {
		if !strings.Contains(s, fn) {
			t.Errorf("common.sh missing %s", fn)
		}
	}
	if !strings.Contains(s, "LOG_FILE_ENABLED") {
		t.Error("common.sh should append stage logs to LOG_FILE when LOG_FILE_ENABLED is set")
	}
}

func TestBackupScriptsEmbedded(t *testing.T) {
	for _, name := range []string{"cleanup.sh", "pgdump.sh", "mysqldump.sh", "redisdump.sh", "compress.sh", "encrypt.sh", "s3-sync.sh"} {
		s, err := BackupScript(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s == "" {
			t.Fatalf("%s is empty", name)
		}
		assertComposedOnce(t, name, s)
	}
}

func TestRestoreScriptsEmbedded(t *testing.T) {
	for _, name := range []string{"cleanup.sh", "fetch.sh", "decrypt.sh", "extract.sh", "pgrestore.sh", "mysqlrestore.sh", "redisrestore.sh"} {
		s, err := RestoreScript(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s == "" {
			t.Fatalf("%s is empty", name)
		}
		assertComposedOnce(t, name, s)
	}
}

func assertComposedOnce(t *testing.T, name, s string) {
	t.Helper()
	if !strings.HasPrefix(s, CommonScript()) {
		t.Errorf("%s: expected common.sh to be prepended", name)
	}
	for _, fn := range []string{"log() {", "wait_for() {", "hold_until_job_done() {", "mark_failed() {"} {
		if n := strings.Count(s, fn); n != 1 {
			t.Errorf("%s: want one %q, got %d", name, fn, n)
		}
	}
}

func TestMariaDBDumpRestoreFlags(t *testing.T) {
	dump, err := BackupScript("mysqldump.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--hex-blob", "--default-character-set=utf8mb4", "--databases", "--max-statement-time=0"} {
		if !strings.Contains(dump, want) {
			t.Errorf("mysqldump.sh missing %q", want)
		}
	}
	restore, err := RestoreScript("mysqlrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GTID_PURGED", "DEFINER=CURRENT_USER", "utf8mb4_unicode_ci", "unique_checks=0"} {
		if !strings.Contains(restore, want) {
			t.Errorf("mysqlrestore.sh missing %q", want)
		}
	}
}

func TestRedisRestoreUsesReplicaOf(t *testing.T) {
	s, err := RestoreScript("redisrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "REPLICAOF") {
		t.Fatal("redisrestore.sh should use REPLICAOF")
	}
	if strings.Contains(s, "MIGRATE") {
		t.Fatal("redisrestore.sh should not SCAN+MIGRATE keys")
	}
	if strings.Contains(s, "FLUSHALL") {
		t.Fatal("redisrestore.sh should not FLUSHALL; REPLICAOF replaces the dataset")
	}
}

func TestCleanupKeepsLogsDir(t *testing.T) {
	backup, err := BackupScript("cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(backup, "! -name logs") {
		t.Error("backup cleanup.sh must not delete the logs/ directory")
	}
	if !strings.Contains(backup, "prune_pipeline_logs") {
		t.Error("backup cleanup.sh should prune expired pipeline logs")
	}
	restore, err := RestoreScript("cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restore, "! -name logs") {
		t.Error("restore cleanup.sh must not delete the logs/ directory")
	}
}

func TestEncryptHonorsS3Enabled(t *testing.T) {
	s, err := BackupScript("encrypt.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "S3_ENABLED") {
		t.Fatal("encrypt.sh should honor S3_ENABLED")
	}
	if !strings.Contains(s, ".step-job-done") {
		t.Fatal("encrypt.sh should write .step-job-done when S3 is disabled")
	}
}

func TestPgRestoreStripsPgAuditEventTriggers(t *testing.T) {
	s, err := RestoreScript("pgrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"strip_pgaudit_ddl",
		"CREATE EVENT TRIGGER",
		"pgaudit_(ddl_command_end|sql_drop)",
		"GRANT[[:space:]].*ON FUNCTION",
		"REVOKE[[:space:]].*ON FUNCTION",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pgrestore.sh missing %q", want)
		}
	}
}

func TestPgDumpDisablesStatementTimeout(t *testing.T) {
	s, err := BackupScript("pgdump.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "statement_timeout=0") {
		t.Fatal("pgdump.sh should disable statement_timeout for long dumps")
	}
	if !strings.Contains(s, "dump_heartbeat_start") {
		t.Fatal("pgdump.sh should heartbeat while pg_dump runs")
	}
}
