package resolver

import (
	"fmt"
	"strings"
	"sync"

	"github.com/safing/portbase/database"
	"github.com/safing/portbase/database/record"
)

const (
	// IPInfoProfileScopeGlobal is the profile scope used for unscoped IPInfo entries.
	IPInfoProfileScopeGlobal = "global"
)

var (
	ipInfoDatabase = database.NewInterface(&database.Options{
		Local:    true,
		Internal: true,

		// Cache entries because new/updated entries will often be queries soon
		// after inserted.
		CacheSize: 256,

		// We only use the cache database here, so we can delay and batch all our
		// writes. Also, no one else accesses these records, so we are fine using
		// this.
		DelayCachedWrites: "cache",
	})
)

// ResolvedDomain holds a Domain name and a list of
// CNAMES that have been resolved.
type ResolvedDomain struct {
	// Domain is the domain as requested by the application.
	Domain string

	// CNAMEs is a list of CNAMEs that have been resolved for
	// Domain.
	CNAMEs []string

	// Expires holds the timestamp when this entry expires.
	// This does not mean that the entry may not be used anymore afterwards,
	// but that this is used to calcuate the TTL of the database record.
	Expires int64
}

// String returns a string representation of ResolvedDomain including
// the CNAME chain. It implements fmt.Stringer
func (resolved *ResolvedDomain) String() string {
	ret := resolved.Domain
	cnames := ""

	if len(resolved.CNAMEs) > 0 {
		cnames = " (-> " + strings.Join(resolved.CNAMEs, "->") + ")"
	}

	return ret + cnames
}

// ResolvedDomains is a helper type for operating on a slice
// of ResolvedDomain
type ResolvedDomains []ResolvedDomain

// String returns a string representation of all domains joined
// to a single string.
func (rds ResolvedDomains) String() string {
	domains := make([]string, len(rds))
	for idx, n := range rds {
		domains[idx] = n.String()
	}
	return strings.Join(domains, " or ")
}

// IPInfo represents various information about an IP.
type IPInfo struct {
	record.Base
	sync.Mutex

	// IP holds the actual IP address.
	IP string

	// ProfileID is used to scope this entry to a process group.
	ProfileID string

	// ResolvedDomain is a slice of domains that
	// have been requested by various applications
	// and have been resolved to IP.
	ResolvedDomains ResolvedDomains
}

// AddDomain adds a new resolved domain to IPInfo.
func (info *IPInfo) AddDomain(resolved ResolvedDomain) {
	info.Lock()
	defer info.Unlock()

	// Delete old for the same domain.
	for idx, d := range info.ResolvedDomains {
		if d.Domain == resolved.Domain {
			info.ResolvedDomains = append(info.ResolvedDomains[:idx], info.ResolvedDomains[idx+1:]...)
			break
		}
	}

	// Add new entry to the end.
	info.ResolvedDomains = append(info.ResolvedDomains, resolved)
}

// MostRecentDomain returns the most recent domain.
func (info *IPInfo) MostRecentDomain() *ResolvedDomain {
	info.Lock()
	defer info.Unlock()

	if len(info.ResolvedDomains) == 0 {
		return nil
	}

	mostRecent := info.ResolvedDomains[len(info.ResolvedDomains)-1]
	return &mostRecent
}

func makeIPInfoKey(profileID, ip string) string {
	return fmt.Sprintf("cache:intel/ipInfo/%s/%s", profileID, ip)
}

// GetIPInfo gets an IPInfo record from the database.
func GetIPInfo(profileID, ip string) (*IPInfo, error) {
	r, err := ipInfoDatabase.Get(makeIPInfoKey(profileID, ip))
	if err != nil {
		return nil, err
	}

	// unwrap
	if r.IsWrapped() {
		// only allocate a new struct, if we need it
		new := &IPInfo{}
		err = record.Unwrap(r, new)
		if err != nil {
			return nil, err
		}

		return new, nil
	}

	// or adjust type
	new, ok := r.(*IPInfo)
	if !ok {
		return nil, fmt.Errorf("record not of type *IPInfo, but %T", r)
	}
	return new, nil
}

// Save saves the IPInfo record to the database.
func (info *IPInfo) Save() error {
	info.Lock()

	// Set database key if not yet set already.
	if !info.KeyIsSet() {
		// Default to global scope if scope is unset.
		if info.ProfileID == "" {
			info.ProfileID = IPInfoProfileScopeGlobal
		}
		info.SetKey(makeIPInfoKey(info.ProfileID, info.IP))
	}

	// Calculate and set cache expiry.
	var expires int64 = 86400 // Minimum TTL of one day.
	for _, rd := range info.ResolvedDomains {
		if rd.Expires > expires {
			expires = rd.Expires
		}
	}
	info.UpdateMeta()
	expires += 3600 // Add one hour to expiry as a buffer.
	info.Meta().SetAbsoluteExpiry(expires)

	info.Unlock()

	return ipInfoDatabase.Put(info)
}

// FmtDomains returns a string consisting of the domains that have seen to use this IP, joined by " or "
func (info *IPInfo) String() string {
	info.Lock()
	defer info.Unlock()

	return fmt.Sprintf("<IPInfo[%s] %s: %s>", info.Key(), info.IP, info.ResolvedDomains.String())
}
