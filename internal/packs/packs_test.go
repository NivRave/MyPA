package packs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse_IgnoresUnknownAndNormalises(t *testing.T) {
	s := Parse(" Baby ,finance,,nonsense")
	assert.True(t, s.Has(Baby))
	assert.True(t, s.Has(Finance))
	assert.False(t, s.Has(House))
	assert.Equal(t, "baby,finance", s.String())
	assert.Equal(t, "", Parse("").String(), "an empty list is Core only")
}

func TestSet_ZeroValueIsCoreOnly(t *testing.T) {
	var s Set
	for _, p := range All() {
		assert.False(t, s.Has(p.ID), p.ID)
		for _, m := range p.Modules {
			assert.False(t, s.ModuleEnabled(m), m)
		}
	}
	assert.True(t, s.ModuleEnabled("memory"), "a Core module is always enabled")
	assert.Equal(t, CoreTopics, s.Topics())
}

func TestSet_WithDoesNotChangeTheOriginal(t *testing.T) {
	s := Parse("baby")
	off := s.With(Baby, false)
	on := s.With(Gmail, true)
	assert.True(t, s.Has(Baby))
	assert.False(t, off.Has(Baby))
	assert.Equal(t, "baby,gmail", on.String())
}

func TestEverything_HasEveryPack(t *testing.T) {
	e := Everything()
	for _, p := range All() {
		assert.True(t, e.Has(p.ID))
	}
	assert.Equal(t, []string{"family", "health", "personal", "work", "baby", "house", "finance"}, e.Topics())
}

func TestTopics_ComeFromThePacks(t *testing.T) {
	s := Parse("finance")
	assert.True(t, s.HasTopic("Finance"))
	assert.False(t, s.HasTopic("baby"))
	assert.True(t, s.HasTopic("health"))
}

func TestPages_BelongToTheirPack(t *testing.T) {
	s := Parse("house")
	assert.True(t, s.PageEnabled("/house"))
	assert.False(t, s.PageEnabled("/baby"))
	assert.False(t, s.PageEnabled("/baby/logs"))
	assert.False(t, s.PageEnabled("/automations"))
	assert.True(t, s.PageEnabled("/memory"), "a Core page is always shown")
	assert.True(t, s.PageEnabled("/babysitter"), "only the route and pages below it belong to the pack")
}

func TestCatalogue_ModulesAndPagesBelongToOnePack(t *testing.T) {
	seenModule, seenPage := map[string]ID{}, map[string]ID{}
	for _, p := range All() {
		assert.NotEmpty(t, p.Name)
		assert.NotEmpty(t, p.Description)
		assert.NotEmpty(t, p.Modules, p.ID)
		for _, m := range p.Modules {
			_, dup := seenModule[m]
			assert.False(t, dup, "module %s is in two packs", m)
			seenModule[m] = p.ID
		}
		for _, pg := range p.Pages {
			_, dup := seenPage[pg]
			assert.False(t, dup, "page %s is in two packs", pg)
			seenPage[pg] = p.ID
		}
	}
}
