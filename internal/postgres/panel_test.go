package postgres_test

import (
	"central-automacoes/internal/domain"
	"central-automacoes/internal/storetest"
	"testing"
)

func TestPostgresPanelContract(t *testing.T) {
	storetest.Panel(t, func(t *testing.T) domain.Store { return openTestStore(t, schemaURL(t)) })
}
