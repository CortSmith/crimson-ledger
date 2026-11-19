package tracker

import (
	"sort"

	"crimsonledger/internal/domain"
)

func sortContacts(c []domain.Contact) {
	sort.Slice(c, func(i, j int) bool { return c[i].GUID < c[j].GUID })
}
