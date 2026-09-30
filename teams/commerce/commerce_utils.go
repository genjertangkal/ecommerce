// Package commerce holds team-internal utilities for the commerce team.
//
// Nothing in //teams/commerce is referenced by another package today. The
// package exists to demonstrate, and test, the team visibility boundary: the
// targets here are visible to //teams:commerce only, so another team adding a
// dependency on them fails at analysis time rather than at review time.
package commerce

// CatalogueSummary is a small, team-internal value type.
type CatalogueSummary struct {
	Team            string
	ProductCount    int
	TotalValueCents int64
}

// Summarise builds a CatalogueSummary from pre-aggregated figures.
//
// The aggregation itself belongs with the commerce service; this is only the
// shape the team shares internally.
func Summarise(team string, productCount int, totalValueCents int64) CatalogueSummary {
	return CatalogueSummary{
		Team:            team,
		ProductCount:    productCount,
		TotalValueCents: totalValueCents,
	}
}
