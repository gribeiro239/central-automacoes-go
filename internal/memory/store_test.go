package memory_test

import (
	"central-automacoes/internal/domain"
	"central-automacoes/internal/memory"
	"central-automacoes/internal/storetest"
	"testing"
)

func TestStoreContract(t *testing.T) {
	storetest.Run(t, func(*testing.T) domain.Store { return memory.New() })
}

func TestSchedulingContract(t *testing.T) {
	storetest.Scheduling(t, func(*testing.T) domain.Store { return memory.New() })
}
