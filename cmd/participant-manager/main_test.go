package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/academic/payment-mesh-experiment/internal/payments"
	"github.com/stretchr/testify/require"
)

func TestCreatePersistsCanonicalParticipantRoles(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &server{db: &payments.DB{SQL: db}}
	mock.ExpectExec("INSERT INTO participants").
		WithArgs("participant-1", "card", "gateway-card", "[\"debtor\",\"creditor\"]", true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	req := httptest.NewRequest(http.MethodPost, "/v1/participants", strings.NewReader(`{"id":" participant-1 ","instrument":"CARD","gateway":" gateway-card ","roles":[" debtor ","creditor","debtor"],"active":true}`))
	response := httptest.NewRecorder()
	s.create(response, req)
	require.Equal(t, http.StatusCreated, response.Code)
	var got payments.Participant
	require.NoError(t, json.NewDecoder(response.Body).Decode(&got))
	require.Equal(t, "participant-1", got.ID)
	require.Equal(t, "card", got.Instrument)
	require.Equal(t, []string{"debtor", "creditor"}, got.Roles)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRejectsUnsupportedInstrumentWithoutDatabaseCall(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &server{db: &payments.DB{SQL: db}}
	req := httptest.NewRequest(http.MethodPost, "/v1/participants", strings.NewReader(`{"id":"p","instrument":"crypto","gateway":"gateway-card","roles":["debtor"]}`))
	response := httptest.NewRecorder()
	s.create(response, req)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRoutingReturnsPersistedRoles(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &server{db: &payments.DB{SQL: db}}
	mock.ExpectQuery("SELECT id,instrument,gateway").WithArgs("p").WillReturnRows(
		sqlmock.NewRows([]string{"id", "instrument", "gateway", "roles", "active"}).
			AddRow("p", "card", "gateway-card", `["creditor"]`, true),
	)
	response := httptest.NewRecorder()
	s.routing(response, httptest.NewRequest(http.MethodGet, "/v1/participants/p", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var got payments.Participant
	require.NoError(t, json.NewDecoder(response.Body).Decode(&got))
	require.Equal(t, []string{"creditor"}, got.Roles)
	require.NoError(t, mock.ExpectationsWereMet())
}
