package memory_test

import (
	"central-automacoes/internal/domain"
	"central-automacoes/internal/memory"
	"central-automacoes/internal/storetest"
	"testing"
)

func TestPanelContract(t *testing.T) {
	storetest.Panel(t, func(*testing.T) domain.Store { return memory.New() })
}
