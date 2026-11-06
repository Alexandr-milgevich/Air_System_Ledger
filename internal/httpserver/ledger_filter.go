package httpserver

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type ledgerFilters struct{ System, Query string }

func parseLedgerFilters(system, query string) (ledgerFilters, error) {
	system = strings.TrimSpace(system)
	query = strings.TrimSpace(query)
	if system != "" && !uuidPattern.MatchString(system) {
		return ledgerFilters{}, errors.New("invalid system")
	}
	if utf8.RuneCountInString(query) > 120 {
		return ledgerFilters{}, errors.New("search too long")
	}
	return ledgerFilters{System: system, Query: query}, nil
}

func ledgerMatches(name, location, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	return query == "" || strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(location), query)
}
