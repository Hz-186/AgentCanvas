package main

import (
	"agentcanvas/internal/pkg/config"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	configPath := os.Getenv("AGENTCANVAS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.local.yaml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configPath = "configs/config.yaml"
		}
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := sql.Open("mysql", cfg.MySQL.DSN)
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping mysql: %v", err)
	}

	files, err := filepath.Glob("migrations/*.up.sql")
	if err != nil {
		log.Fatalf("find migrations: %v", err)
	}
	sort.Strings(files)

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			log.Fatalf("read migration %s: %v", file, err)
		}
		for _, stmt := range splitSQLStatements(string(data)) {
			if _, err := db.Exec(stmt); err != nil {
				log.Fatalf("exec migration %s: %v\nstatement: %s", file, err, stmt)
			}
		}
		fmt.Printf("applied %s\n", file)
	}
}

func splitSQLStatements(sqlText string) []string {
	lines := strings.Split(sqlText, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleaned = append(cleaned, line)
	}

	parts := strings.Split(strings.Join(cleaned, "\n"), ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		statements = append(statements, stmt)
	}
	return statements
}
