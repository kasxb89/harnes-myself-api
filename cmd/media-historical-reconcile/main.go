// media-historical-reconcile is an explicit operator-only, finite historical
// repair command. Opening a database never initializes or migrates the server.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "inspect", "inspect, canonicalize, plan, prepare-schema or apply")
	dialect := flag.String("dialect", "sqlite", "sqlite, mysql or postgres")
	manifestPath := flag.String("manifest", "", "private manifest file")
	expectedSHA := flag.String("manifest-sha256", "", "explicit SHA256 of the canonical manifest")
	idsFlag := flag.String("task-ids", "", "explicit sorted task IDs from the frozen private manifest")
	prepareFlag := flag.Bool("prepare-exact-historical-schema", false, "required together with mode=prepare-schema")
	applyFlag := flag.Bool("apply-exact-historical-manifest", false, "required together with mode=apply")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *mode == "canonicalize" {
		if *manifestPath == "" || *applyFlag {
			return fmt.Errorf("canonicalize requires only a manifest")
		}
		data, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		_, canonical, err := model.CanonicalMediaHistoricalManifest(data)
		if err != nil {
			return err
		}
		fmt.Print(string(canonical))
		return nil
	}
	if *mode != "inspect" && *mode != "plan" && *mode != "apply" && *mode != "prepare-schema" {
		return fmt.Errorf("unsupported executable mode")
	}
	if (*mode == "prepare-schema") != *prepareFlag {
		return fmt.Errorf("prepare-schema requires its explicit flag")
	}
	if (*mode == "apply") != *applyFlag {
		return fmt.Errorf("apply requires both mode=apply and --apply-exact-historical-manifest")
	}
	if strings.TrimSpace(*idsFlag) == "" {
		return fmt.Errorf("explicit task-ids required")
	}
	ids := []int64{}
	for value := range strings.SplitSeq(*idsFlag, ",") {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	dsn := os.Getenv("MEDIA_HISTORICAL_DSN")
	if dsn == "" {
		return fmt.Errorf("MEDIA_HISTORICAL_DSN must explicitly identify the intended database")
	}
	var driver gorm.Dialector
	switch *dialect {
	case "sqlite":
		// Inspection uses SQLite's own read-only URI in addition to not calling any
		// migration API. Apply may write only an existing explicitly selected file.
		name := strings.TrimPrefix(strings.Split(dsn, "?")[0], "file:")
		if !strings.HasPrefix(name, "/") {
			return fmt.Errorf("SQLite database must be an absolute path")
		}
		info, err := os.Stat(name)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite database must already exist")
		}
		if *mode != "apply" && *mode != "prepare-schema" {
			dsn = "file:" + name + "?mode=ro&_pragma=busy_timeout(30000)"
		} else {
			dsn = "file:" + name + "?mode=rw&_pragma=busy_timeout(30000)&_txlock=immediate"
		}
		driver = sqlite.Open(dsn)
	case "mysql":
		driver = mysql.Open(dsn)
	case "postgres":
		driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	default:
		return fmt.Errorf("unsupported database dialect")
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return fmt.Errorf("database open failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	common.SetDatabaseTypes(common.DatabaseType(*dialect), common.DatabaseType(*dialect))
	common.RedisEnabled = false
	model.DB, model.LOG_DB = db, db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if *mode == "inspect" {
		if *manifestPath != "" || *expectedSHA != "" {
			return fmt.Errorf("inspect does not accept an apply manifest")
		}
		manifest, err := model.InspectMediaHistoricalReconciliation(ctx, ids)
		if err != nil {
			return err
		}
		data, err := common.Marshal(manifest)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	if *manifestPath == "" || len(*expectedSHA) != 64 {
		return fmt.Errorf("plan/apply requires manifest and manifest-sha256")
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != *expectedSHA {
		return fmt.Errorf("manifest file SHA256 mismatch; freeze exact canonical bytes without trailing newline")
	}
	if *mode == "prepare-schema" {
		if err := model.PrepareMediaHistoricalReconciliationSchema(ctx, data, *expectedSHA, ids); err != nil {
			return err
		}
		fmt.Println(`{"status":"HISTORICAL_RECEIPT_SCHEMA_PREPARED"}`)
		return nil
	}
	result, err := model.ReconcileMediaHistoricalManifest(ctx, data, *expectedSHA, ids, *mode == "apply")
	if err != nil {
		return err
	}
	// This standalone command has no Redis settings or keys. A live apply must
	// invalidate caches in the API process or run while the API is stopped, then
	// reopen traffic only after the normal cache-free startup. State this truth.
	if *mode == "apply" {
		result.CacheInvalidationPending = true
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
