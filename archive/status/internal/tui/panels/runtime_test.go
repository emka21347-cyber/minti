package panels

import (
	"testing"
	"time"

	"github.com/minti/status/internal/probes/runtime"
)

func TestRuntime_Healthy(t *testing.T) {
	st := runtime.Status{
		Healthy:  true,
		Version:  "0.1.0-M3",
		Backend:  "ollama",
		Endpoint: "http://127.0.0.1:7780",
		Models: []runtime.Model{
			{Name: "hermes3:8b", Resident: true, ReasoningScore: 78},
			{Name: "mistral:7b", Resident: true, ReasoningScore: 62},
			{Name: "llama3.2:3b", Resident: true, ReasoningScore: 35},
		},
	}
	vram := []runtime.LoadedModel{
		{Name: "hermes3:8b", SizeGB: 4.9, TTLLeft: 4*time.Minute + 31*time.Second},
	}
	got := Runtime(st, vram)
	assertGolden(t, "runtime_healthy", got)
}

func TestRuntime_Down(t *testing.T) {
	st := runtime.Status{Healthy: false, Endpoint: "http://127.0.0.1:7780"}
	got := Runtime(st, nil)
	assertGolden(t, "runtime_down", got)
}
