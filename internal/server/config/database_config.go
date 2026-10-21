//
//  Copyright 2026 The InfiniFlow Authors. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.
//

package config

import (
	"fmt"
	"net"
	"net/url"
	"ragflow/internal/common"
	"strconv"

	"github.com/spf13/viper"
)

// DatabaseConfig database configuration
type DatabaseConfig struct {
	MySQL    MySQLConfig    `mapstructure:"mysql"`
	Postgres PostgresConfig `mapstructure:"postgres"`
}

type MySQLConfig struct {
	DatabaseName     string `mapstructure:"name"` // database name
	User             string `mapstructure:"user"`
	Password         string `mapstructure:"password"`
	Host             string `mapstructure:"host"`
	Port             int    `mapstructure:"port"`
	MaxConnections   int    `mapstructure:"max_connections"`
	StaleTimeout     int    `mapstructure:"stale_timeout"`
	MaxAllowedPacket int    `mapstructure:"max_allowed_packet"`
	Charset          string `mapstructure:"charset"`
}

func (c *Config) ParseDatabaseConfig(v *viper.Viper) error {
	databaseType := c.general.Database
	if envType := common.GetEnvSmall(common.EnvDBType); envType != "" {
		databaseType = envType
	}
	if databaseType == "" {
		databaseType = "postgres"
	}
	if databaseType != "postgres" {
		return fmt.Errorf("metadata runtime requires PostgreSQL; use pg_migrate for offline MySQL import")
	}
	return c.parsePostgresConfig(v)
}

func (c *Config) parseOceanBaseDatabaseConfig(v *viper.Viper) {
	// OceanBase uses the MySQL wire protocol, so the existing DAO can keep
	// consuming MySQLConfig. Support both the flat DB_TYPE=oceanbase section and
	// the document-engine nested config.
	c.parseMySQLConfig(v)
	sub := v.Sub("oceanbase")
	if sub == nil {
		return
	}
	if nested := sub.Sub("config"); nested != nil {
		if nested.IsSet("db_name") {
			c.database.MySQL.DatabaseName = nested.GetString("db_name")
		}
		sub = nested
	}
	if sub.IsSet("name") {
		c.database.MySQL.DatabaseName = sub.GetString("name")
	}
	if sub.IsSet("db_name") {
		c.database.MySQL.DatabaseName = sub.GetString("db_name")
	}
	if sub.IsSet("user") {
		c.database.MySQL.User = sub.GetString("user")
	}
	if sub.IsSet("password") {
		c.database.MySQL.Password = sub.GetString("password")
	}
	if sub.IsSet("host") {
		c.database.MySQL.Host = sub.GetString("host")
	}
	if sub.IsSet("port") {
		c.database.MySQL.Port = sub.GetInt("port")
	}
	if sub.IsSet("max_connections") {
		c.database.MySQL.MaxConnections = sub.GetInt("max_connections")
	}
}

func (c *Config) parseMySQLConfig(v *viper.Viper) {

	// Default MySQL config
	c.database.MySQL.DatabaseName = "rag_flow"
	c.database.MySQL.User = "root"
	c.database.MySQL.Password = "infini_rag_flow"
	c.database.MySQL.Host = "localhost"
	c.database.MySQL.Port = 3306
	c.database.MySQL.MaxConnections = 900
	c.database.MySQL.StaleTimeout = 300
	c.database.MySQL.MaxAllowedPacket = 1073741824
	c.database.MySQL.Charset = "utf8mb4"

	if !v.IsSet("mysql") {
		return
	}
	sub := v.Sub("mysql")
	if sub == nil {
		return
	}

	if sub.IsSet("name") {
		c.database.MySQL.DatabaseName = sub.GetString("name")
	}

	if sub.IsSet("user") {
		c.database.MySQL.User = sub.GetString("user")
	}

	if sub.IsSet("password") {
		c.database.MySQL.Password = sub.GetString("password")
	}

	if sub.IsSet("host") {
		c.database.MySQL.Host = sub.GetString("host")
	}

	if sub.IsSet("port") {
		c.database.MySQL.Port = sub.GetInt("port")
	}

	if sub.IsSet("max_connections") {
		c.database.MySQL.MaxConnections = sub.GetInt("max_connections")
	}

	if sub.IsSet("stale_timeout") {
		c.database.MySQL.StaleTimeout = sub.GetInt("stale_timeout")
	}

	if sub.IsSet("max_allowed_packet") {
		c.database.MySQL.MaxAllowedPacket = sub.GetInt("max_allowed_packet")
	}

	if sub.IsSet("charset") {
		c.database.MySQL.Charset = sub.GetString("charset")
	}
}

func (c *Config) GetMySQLConfig() MySQLConfig {
	return c.database.MySQL
}

func (m *MySQLConfig) ExportConfigs() map[string]interface{} {
	var mysqlConfigs map[string]interface{}
	mysqlConfigs = make(map[string]interface{})
	mysqlConfigs["host"] = m.Host
	mysqlConfigs["port"] = m.Port
	mysqlConfigs["database_name"] = m.DatabaseName
	mysqlConfigs["username"] = m.User
	mysqlConfigs["password"] = m.Password
	mysqlConfigs["charset"] = m.Charset
	mysqlConfigs["max_connections"] = m.MaxConnections
	mysqlConfigs["stale_timeout"] = m.StaleTimeout
	mysqlConfigs["max_allowed_packet"] = m.MaxAllowedPacket
	return mysqlConfigs
}

// PostgresConfig configures the native PostgreSQL metadata backend.
type PostgresConfig struct {
	DatabaseName   string `mapstructure:"name"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	SSLMode        string `mapstructure:"sslmode"`
	MaxConnections int    `mapstructure:"max_connections"`
	StaleTimeout   int    `mapstructure:"stale_timeout"`
}

func (c *Config) parsePostgresConfig(v *viper.Viper) error {
	p := PostgresConfig{DatabaseName: "ragflow", User: "ragflow", Host: "postgres", Port: 5432, SSLMode: "verify-full", MaxConnections: 10, StaleTimeout: 300}
	if err := v.UnmarshalKey("postgres", &p); err != nil {
		return fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}
	for name, target := range map[string]*string{"NAME": &p.DatabaseName, "USER": &p.User, "PASSWORD": &p.Password, "HOST": &p.Host, "SSLMODE": &p.SSLMode} {
		if value := common.GetEnv("RAGFLOW_POSTGRES_" + name); value != "" {
			*target = value
		}
	}
	if value := common.GetEnv("RAGFLOW_POSTGRES_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid PostgreSQL port")
		}
		p.Port = port
	}
	if p.Host == "" || p.DatabaseName == "" || p.User == "" || p.Password == "" {
		return fmt.Errorf("PostgreSQL host, name, user and password are required")
	}
	if p.Port < 1 || p.Port > 65535 || p.MaxConnections < 1 || p.StaleTimeout < 1 {
		return fmt.Errorf("invalid PostgreSQL pool or port configuration")
	}
	switch p.SSLMode {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("invalid PostgreSQL sslmode")
	}
	c.database.Postgres = p
	return nil
}
func (c *Config) GetPostgresConfig() PostgresConfig { return c.database.Postgres }
func (p PostgresConfig) DSN() string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(p.User, p.Password), Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), Path: "/" + p.DatabaseName}
	q := u.Query()
	q.Set("sslmode", p.SSLMode)
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}

func (p PostgresConfig) ExportConfigs() map[string]interface{} {
	return map[string]interface{}{"host": p.Host, "port": p.Port, "database_name": p.DatabaseName, "username": p.User, "password": p.Password, "sslmode": p.SSLMode, "max_connections": p.MaxConnections, "stale_timeout": p.StaleTimeout}
}
