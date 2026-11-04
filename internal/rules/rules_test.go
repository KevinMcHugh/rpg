package rules_test

import (
	"slices"
	"testing"

	"github.com/KevinMcHugh/rpg/internal/rules"
)

// fixed rolls the dice given, in order.
func fixed(dice ...int) func(int) int {
	i := 0
	return func(int) int { d := dice[i]; i++; return d }
}

func ruleset(t *testing.T, id string) rules.Ruleset {
	t.Helper()
	r, err := rules.For(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMod(t *testing.T) {
	for score, want := range map[int]int{1: -5, 8: -1, 9: -1, 10: 0, 11: 0, 12: 1, 15: 2, 20: 5} {
		if got := rules.Mod(score); got != want {
			t.Errorf("Mod(%d) = %d, want %d", score, got, want)
		}
	}
	for level, want := range map[int]int{1: 2, 4: 2, 5: 3, 9: 4, 17: 6, 20: 6} {
		if got := rules.Proficiency(level); got != want {
			t.Errorf("Proficiency(%d) = %d, want %d", level, got, want)
		}
	}
}

func TestBonuses(t *testing.T) {
	for _, tc := range []struct {
		ruleset string
		check   rules.Check
		want    int
	}{
		{"classic", rules.Check{Skill: 3}, 3},
		{"srd52", rules.Check{Skill: 0, Ability: 14, Level: 1}, 2},         // +2 Wis, not proficient
		{"srd52", rules.Check{Skill: 1, Ability: 14, Level: 1}, 4},         // proficient: +2
		{"srd52", rules.Check{Skill: 2, Ability: 14, Level: 5}, 2 + 3*2},   // expertise at level 5
		{"srd35", rules.Check{Skill: 4, Ability: 8}, 3},                    // 4 ranks, -1
		{"pf2e", rules.Check{Skill: 0, Ability: 3, Level: 5}, 3},           // untrained: attribute only
		{"pf2e", rules.Check{Skill: 1, Ability: 3, Level: 5}, 3 + 5 + 2},   // trained
		{"pf2e", rules.Check{Skill: 4, Ability: 4, Level: 15}, 4 + 15 + 8}, // legendary
	} {
		if got := ruleset(t, tc.ruleset).Bonus(tc.check); got != tc.want {
			t.Errorf("%s %+v: bonus %d, want %d", tc.ruleset, tc.check, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		ruleset string
		check   rules.Check
		dice    []int
		want    rules.Degree
	}{
		{"classic", rules.Check{Skill: 2, Difficulty: 8}, []int{3, 3}, rules.Success},
		{"classic", rules.Check{Skill: 2, Difficulty: 8}, []int{2, 3}, rules.Failure},
		{"srd52", rules.Check{Skill: 1, Ability: 10, Level: 1, Difficulty: 15}, []int{13}, rules.Success},
		{"srd52", rules.Check{Skill: 1, Ability: 10, Level: 1, Difficulty: 15}, []int{12}, rules.Failure},
		// 3.5: a natural 20 is not an automatic success on a skill check.
		{"srd35", rules.Check{Skill: 0, Ability: 10, Difficulty: 25}, []int{20}, rules.Failure},
		// pf2e: beat the DC by 10, miss it by 10, and the natural 20 and 1.
		{"pf2e", rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}, []int{19}, rules.Success},         // 22: short of 25
		{"pf2e", rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}, []int{20}, rules.CriticalSuccess}, // 23, up one
		{"pf2e", rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}, []int{12}, rules.Success},         // 15
		{"pf2e", rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}, []int{11}, rules.Failure},         // 14
		{"pf2e", rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}, []int{2}, rules.CriticalFailure},  // 5
		{"pf2e", rules.Check{Skill: 0, Ability: 0, Difficulty: 30}, []int{20}, rules.CriticalFailure + 1},       // 20 vs 30: crit fail, up one
		{"pf2e", rules.Check{Skill: 4, Ability: 5, Level: 20, Difficulty: 10}, []int{1}, rules.Success},         // 34: crit success, down one
	} {
		got := ruleset(t, tc.ruleset).Resolve(tc.check, fixed(tc.dice...))
		if got.Degree != tc.want {
			t.Errorf("%s %+v rolling %v: %s (total %d), want %s", tc.ruleset, tc.check, tc.dice, got.Degree, got.Total, tc.want)
		}
	}
}

func TestChances(t *testing.T) {
	for _, tc := range []struct {
		ruleset string
		check   rules.Check
		want    []int
	}{
		{"classic", rules.Check{Skill: 2, Difficulty: 8}, []int{27, 72}},
		{"srd52", rules.Check{Skill: 1, Ability: 10, Level: 1, Difficulty: 15}, []int{60, 40}},
		{"srd35", rules.Check{Skill: 0, Ability: 10, Difficulty: 25}, []int{100, 0}},
		// Bonus 3 vs DC 15: 1-2 critically fail (total ≤ 5), 3-11 fail,
		// 12-19 succeed, and 20 (23) succeeds and steps up to critical.
		{"pf2e", rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}, []int{10, 45, 40, 5}},
	} {
		r := ruleset(t, tc.ruleset)
		got := r.Chances(tc.check)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s %+v: chances %v, want %v", tc.ruleset, tc.check, got, tc.want)
		}
	}
	if got := rules.PassChance(ruleset(t, "pf2e"), rules.Check{Skill: 1, Ability: 0, Level: 1, Difficulty: 15}); got != 45 {
		t.Errorf("pf2e pass chance %d, want 45", got)
	}
	if _, err := rules.For("gurps"); err == nil {
		t.Error("an unknown ruleset was found")
	}
}
