package globalcomix_test

import (
	"testing"

	"github.com/tamnd/globalcomix-cli/globalcomix"
)

func TestDomainInfo(t *testing.T) {
	info := globalcomix.Domain{}.Info()
	if info.Scheme != "globalcomix" {
		t.Errorf("Scheme = %q, want globalcomix", info.Scheme)
	}
	found := false
	for _, h := range info.Hosts {
		if h == "globalcomix.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Hosts %v does not contain globalcomix.com", info.Hosts)
	}
	if info.Identity.Binary != "gc" {
		t.Errorf("Identity.Binary = %q, want gc", info.Identity.Binary)
	}
	if info.Identity.Short == "" {
		t.Error("Identity.Short is empty")
	}
}

func TestDomainAliases(t *testing.T) {
	info := globalcomix.Domain{}.Info()
	found := false
	for _, a := range info.Aliases {
		if a == "gc" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Aliases %v does not contain gc", info.Aliases)
	}
}
