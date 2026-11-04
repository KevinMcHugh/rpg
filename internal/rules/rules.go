// Package rules is how a game's checks resolve. Each game picks a ruleset
// (games.ruleset); the engine (package game) asks it to roll a check and
// say how well the player did. A ruleset is mechanics only: dice, bonuses
// and degrees of success. Mechanics are not copyrightable, so nothing here
// needs a license; the words a game shows (skill names and so on) are its
// content, written by its author or imported with a license
// (docs/mechanics.md).
//
// A check is a skill against a difficulty. What a skill's value means
// depends on the ruleset:
//
//	classic  2d6 + skill level                                   vs difficulty
//	srd52    d20 + ability modifier + proficiency bonus × tier   vs DC   (tier 0 none, 1 proficient, 2 expertise)
//	srd35    d20 + skill ranks + ability modifier                 vs DC
//	pf2e     d20 + attribute modifier + proficiency              vs DC   (rank 0 untrained … 4 legendary; trained or better adds level + 2 × rank)
//
// Only pf2e has critical degrees: beating the DC by 10 is a critical
// success, missing it by 10 a critical failure, and a natural 20 or 1
// moves the result one degree up or down.
package rules

import (
	"fmt"
	"slices"
)

// Degree is how well a check went.
type Degree int

const (
	CriticalFailure Degree = iota - 2
	Failure
	_
	Success
	CriticalSuccess
)

// String is the degree's name in logs and saves.
func (d Degree) String() string {
	switch d {
	case CriticalFailure:
		return "critical_failure"
	case Failure:
		return "failure"
	case Success:
		return "success"
	case CriticalSuccess:
		return "critical_success"
	}
	return fmt.Sprintf("degree(%d)", int(d))
}

// Passed reports whether the check succeeded, critically or not.
func (d Degree) Passed() bool { return d >= Success }

// Abilities are the six ability keys the d20 rulesets share. Scripts name
// them (ability str >= 14), and a skill names the one it adds.
var Abilities = []string{"str", "dex", "con", "int", "wis", "cha"}

// Check is what a check needs from the player and the content.
type Check struct {
	Skill      int // the skill's value: level, proficiency tier, ranks or proficiency rank
	Ability    int // the governing ability's value: a score (srd52, srd35) or a modifier (pf2e); unused by classic
	Level      int // the character's level; srd52 and pf2e use it
	Difficulty int
}

// Result is a rolled check.
type Result struct {
	Dice   []int
	Bonus  int
	Total  int
	Degree Degree
}

// Ruleset resolves checks for one ruleset.
type Ruleset interface {
	ID() string
	// Degrees are the outcomes a check can have, worst first.
	Degrees() []Degree
	// Bonus is what the player adds to the dice.
	Bonus(c Check) int
	// Resolve rolls c, drawing each die from roll(sides).
	Resolve(c Check, roll func(sides int) int) Result
	// Chances is the percent chance of each degree, in Degrees order.
	Chances(c Check) []int
	// InitialAbility is an ability's value before anything changes it.
	InitialAbility() int
	// SkillMax bounds a skill's value: classic levels are open-ended (0).
	SkillMax() int
}

// For is the ruleset with id, or an error naming the ones there are.
func For(id string) (Ruleset, error) {
	for _, r := range all {
		if r.ID() == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no ruleset %q (there is %v)", id, IDs())
}

// IDs are the rulesets there are.
func IDs() []string {
	out := make([]string, len(all))
	for i, r := range all {
		out[i] = r.ID()
	}
	return out
}

var all = []Ruleset{classic{}, srd52{}, srd35{}, pf2e{}}

// Mod is a d20 ability score's modifier: 10 and 11 are +0, 12 is +1.
func Mod(score int) int {
	d := score - 10
	if d < 0 {
		return -((-d + 1) / 2)
	}
	return d / 2
}

var passFail = []Degree{Failure, Success}

// ---- classic ----------------------------------------------------------------

type classic struct{}

func (classic) ID() string          { return "classic" }
func (classic) Degrees() []Degree   { return passFail }
func (classic) Bonus(c Check) int   { return c.Skill }
func (classic) InitialAbility() int { return 0 }
func (classic) SkillMax() int       { return 0 }

func (r classic) Resolve(c Check, roll func(int) int) Result {
	dice := []int{roll(6), roll(6)}
	total := dice[0] + dice[1] + r.Bonus(c)
	return Result{Dice: dice, Bonus: r.Bonus(c), Total: total, Degree: passOrFail(total, c.Difficulty)}
}

func (r classic) Chances(c Check) []int {
	pass := 0
	for a := 1; a <= 6; a++ {
		for b := 1; b <= 6; b++ {
			if a+b+r.Bonus(c) >= c.Difficulty {
				pass++
			}
		}
	}
	return percents([]int{36 - pass, pass}, 36)
}

// ---- d20 pass/fail: srd52 and srd35 -----------------------------------------

// d20 is a ruleset that rolls one d20 against a DC, and passes or fails.
type d20 struct{ bonus func(Check) int }

func (r d20) resolve(c Check, roll func(int) int) Result {
	die := roll(20)
	total := die + r.bonus(c)
	return Result{Dice: []int{die}, Bonus: r.bonus(c), Total: total, Degree: passOrFail(total, c.Difficulty)}
}

func (r d20) chances(c Check) []int {
	pass := 0
	for die := 1; die <= 20; die++ {
		if die+r.bonus(c) >= c.Difficulty {
			pass++
		}
	}
	return percents([]int{20 - pass, pass}, 20)
}

type srd52 struct{}

// Proficiency is SRD 5.2's proficiency bonus at a level: +2 at levels 1 to
// 4, rising by one every four levels.
func Proficiency(level int) int {
	if level < 1 {
		level = 1
	}
	return 2 + (level-1)/4
}

func (srd52) ID() string          { return "srd52" }
func (srd52) Degrees() []Degree   { return passFail }
func (srd52) InitialAbility() int { return 10 }
func (srd52) SkillMax() int       { return 2 }
func (srd52) Bonus(c Check) int   { return Mod(c.Ability) + Proficiency(c.Level)*clamp(c.Skill, 0, 2) }
func (r srd52) Resolve(c Check, roll func(int) int) Result {
	return d20{r.Bonus}.resolve(c, roll)
}
func (r srd52) Chances(c Check) []int { return d20{r.Bonus}.chances(c) }

type srd35 struct{}

func (srd35) ID() string          { return "srd35" }
func (srd35) Degrees() []Degree   { return passFail }
func (srd35) InitialAbility() int { return 10 }
func (srd35) SkillMax() int       { return 0 }
func (srd35) Bonus(c Check) int   { return c.Skill + Mod(c.Ability) }
func (r srd35) Resolve(c Check, roll func(int) int) Result {
	// A natural 20 is not an automatic success on a 3.5 skill check.
	return d20{r.Bonus}.resolve(c, roll)
}
func (r srd35) Chances(c Check) []int { return d20{r.Bonus}.chances(c) }

// ---- pf2e: four degrees -----------------------------------------------------

type pf2e struct{}

func (pf2e) ID() string { return "pf2e" }
func (pf2e) Degrees() []Degree {
	return []Degree{CriticalFailure, Failure, Success, CriticalSuccess}
}
func (pf2e) InitialAbility() int { return 0 } // Remaster attributes are modifiers
func (pf2e) SkillMax() int       { return 4 }

// Bonus is the attribute modifier plus proficiency: untrained adds nothing,
// trained or better adds the level and 2 per rank.
func (pf2e) Bonus(c Check) int {
	rank := clamp(c.Skill, 0, 4)
	if rank == 0 {
		return c.Ability
	}
	return c.Ability + c.Level + 2*rank
}

func (r pf2e) degree(die int, c Check) Degree {
	total := die + r.Bonus(c)
	d := Failure
	switch {
	case total >= c.Difficulty+10:
		d = CriticalSuccess
	case total >= c.Difficulty:
		d = Success
	case total <= c.Difficulty-10:
		d = CriticalFailure
	}
	// A natural 20 or 1 moves the result one degree.
	switch die {
	case 20:
		d = step(d, +1)
	case 1:
		d = step(d, -1)
	}
	return d
}

func (r pf2e) Resolve(c Check, roll func(int) int) Result {
	die := roll(20)
	return Result{Dice: []int{die}, Bonus: r.Bonus(c), Total: die + r.Bonus(c), Degree: r.degree(die, c)}
}

func (r pf2e) Chances(c Check) []int {
	counts := make([]int, 4)
	order := r.Degrees()
	for die := 1; die <= 20; die++ {
		counts[slices.Index(order, r.degree(die, c))]++
	}
	return percents(counts, 20)
}

func step(d Degree, by int) Degree {
	order := []Degree{CriticalFailure, Failure, Success, CriticalSuccess}
	i := clamp(slices.Index(order, d)+by, 0, 3)
	return order[i]
}

// ---- helpers ----------------------------------------------------------------

func passOrFail(total, difficulty int) Degree {
	if total >= difficulty {
		return Success
	}
	return Failure
}

// percents turns counts out of total into whole percents.
func percents(counts []int, total int) []int {
	out := make([]int, len(counts))
	for i, n := range counts {
		out[i] = n * 100 / total
	}
	return out
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

// PassChance is the percent chance a check passes, critically or not.
func PassChance(r Ruleset, c Check) int {
	sum := 0
	for i, d := range r.Degrees() {
		if d.Passed() {
			sum += r.Chances(c)[i]
		}
	}
	return sum
}
