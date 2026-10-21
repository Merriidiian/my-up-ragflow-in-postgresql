package router

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"os"
	"ragflow/internal/handler"
	"testing"
)

// Route registration is not a successful live function check. Export all OSS
// routes so unexecuted operations stay visible in the coverage report.
func TestExportPostgresFunctionInventory(t *testing.T) {
	path := os.Getenv("VSM_ROUTE_REPORT")
	if path == "" {
		t.Skip("explicit inventory export only")
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := &Router{authHandler: handler.NewAuthHandler(), agentHandler: &handler.AgentHandler{}}
	r.Setup(engine)
	result := []map[string]string{}
	for _, route := range engine.Routes() {
		result = append(result, map[string]string{"method": route.Method, "path": route.Path, "handler": route.Handler, "live_status": "not_individually_executed"})
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
