package config

import (
	"github.com/spf13/viper"
	"net/url"
	"testing"
)

func TestPostgresConfig(t *testing.T) {
	t.Setenv("DB_TYPE", "postgres")
	t.Setenv("RAGFLOW_POSTGRES_PASSWORD", "p@ss:/?#%")
	v := viper.New()
	v.Set("postgres.sslmode", "disable")
	c := &Config{}
	if err := c.ParseDatabaseConfig(v); err != nil {
		t.Fatal(err)
	}
	p := c.GetPostgresConfig()
	u, err := url.Parse(p.DSN())
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if password != "p@ss:/?#%" || u.Host != "postgres:5432" || u.Query().Get("timezone") != "UTC" {
		t.Fatal("invalid escaped DSN")
	}
	t.Setenv("RAGFLOW_POSTGRES_PORT", "invalid")
	if err := c.ParseDatabaseConfig(v); err == nil {
		t.Fatal("accepted invalid port")
	}
	t.Setenv("RAGFLOW_POSTGRES_PORT", "5432")
	v.Set("postgres.sslmode", "unknown")
	if err := c.ParseDatabaseConfig(v); err == nil {
		t.Fatal("accepted invalid sslmode")
	}
}

// The import CLI is the only metadata entry point allowed to open MySQL.
func TestPostgresOnlyMetadataRuntime(t *testing.T) {
	for _, backend := range []string{"mysql", "oceanbase", "sqlite", "unknown"} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("DB_TYPE", backend)
			c := &Config{}
			if err := c.ParseDatabaseConfig(viper.New()); err == nil {
				t.Fatal("accepted non-PostgreSQL metadata")
			}
			if err := c.GetEnvironments(); err == nil {
				t.Fatal("accepted non-PostgreSQL environment")
			}
		})
	}
	t.Setenv("DB_TYPE", "")
	t.Setenv("RAGFLOW_POSTGRES_PASSWORD", "test-only-password")
	c := &Config{}
	v := viper.New()
	if err := c.ParseGeneralConfig(v); err != nil {
		t.Fatal(err)
	}
	if c.DatabaseType() != "postgres" {
		t.Fatal("default must be PostgreSQL")
	}
	if err := c.ParseDatabaseConfig(v); err != nil {
		t.Fatal(err)
	}
	c.general.Database = "mysql"
	if err := c.ParseDatabaseConfig(v); err == nil {
		t.Fatal("accepted MySQL YAML setting")
	}
	t.Setenv("DB_TYPE", "postgres")
	if err := c.ParseDatabaseConfig(v); err != nil {
		t.Fatal("explicit PostgreSQL override:", err)
	}
	t.Setenv("RAGFLOW_POSTGRES_PASSWORD", "")
	if err := c.ParseDatabaseConfig(v); err == nil {
		t.Fatal("accepted missing PostgreSQL credential")
	}
}
