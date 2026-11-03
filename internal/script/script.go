// Package script is the small language authors use for conditions and
// effects: when a choice shows, what taking it changes. Authors type text
// on the pages and over the API; it is stored as JSON that refers to flags,
// skills, characters, quests and conversations by id, so renaming a key
// never breaks a script. Parse turns text into the stored form, and Format
// turns it back, with today's keys.
//
// Conditions:
//
//	met_mayor                      a flag is set (not 0)
//	not met_mayor                  ... is not
//	gold >= 10                     a flag compared with a number (== != >= <= > <)
//	skill persuasion >= 3          the player's skill level
//	approval aria > 10             a character's approval of the player
//	quest find_brother >= 2        a quest's stage number (0 is not started)
//	quest find_brother done        a quest reached a stage that completes it
//	done docks_intro               a conversation was finished at least once
//	a and (b or not c)             combined; and binds tighter than or
//
// Effects, separated by ";" or new lines:
//
//	met_mayor                      set a flag to 1
//	met_mayor = false              set it (true is 1, false is 0)
//	gold += 10                     add to it (-= subtracts)
//	skill persuasion += 1          raise a skill
//	approval aria -= 5             change a character's approval
//	quest find_brother = 2         move a quest to a stage
//
// See docs/mechanics.md.
package script

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Kinds of thing a script refers to.
const (
	KindFlag         = "flag"
	KindSkill        = "skill"
	KindCharacter    = "character"
	KindQuest        = "quest"
	KindConversation = "conversation"
)

// Cond is a stored condition. Exactly one group of fields is set: All, Any
// or Not combine others; Flag, Skill, Approval or Quest compare with Op and
// Value; Quest with Op "done" checks completion; Done names a conversation.
type Cond struct {
	All      []Cond `json:"all,omitempty"`
	Any      []Cond `json:"any,omitempty"`
	Not      *Cond  `json:"not,omitempty"`
	Flag     string `json:"flag,omitempty"`
	Skill    string `json:"skill,omitempty"`
	Approval string `json:"approval,omitempty"`
	Quest    string `json:"quest,omitempty"`
	Done     string `json:"done,omitempty"`
	Op       string `json:"op,omitempty"`
	Value    int    `json:"value,omitempty"`
}

// Effect is one stored effect. Exactly one of Flag, Skill, Approval and
// Quest is set; Op is "=", "+=" or "-=" (always "=" for a quest).
type Effect struct {
	Flag     string `json:"flag,omitempty"`
	Skill    string `json:"skill,omitempty"`
	Approval string `json:"approval,omitempty"`
	Quest    string `json:"quest,omitempty"`
	Op       string `json:"op"`
	Value    int    `json:"value"`
}

// Ref is something a script refers to.
type Ref struct{ Kind, ID string }

// Refs is everything c refers to.
func (c Cond) Refs() []Ref {
	var out []Ref
	c.walk(func(c Cond) {
		switch {
		case c.Flag != "":
			out = append(out, Ref{KindFlag, c.Flag})
		case c.Skill != "":
			out = append(out, Ref{KindSkill, c.Skill})
		case c.Approval != "":
			out = append(out, Ref{KindCharacter, c.Approval})
		case c.Quest != "":
			out = append(out, Ref{KindQuest, c.Quest})
		case c.Done != "":
			out = append(out, Ref{KindConversation, c.Done})
		}
	})
	return out
}

// QuestStage is a quest and one of its stage numbers.
type QuestStage struct {
	Quest string
	Stage int
}

// QuestStages is every stage c compares a quest with, for checking that
// the stages exist.
func (c Cond) QuestStages() []QuestStage {
	var out []QuestStage
	c.walk(func(c Cond) {
		if c.Quest != "" && c.Op != "done" && c.Value > 0 {
			out = append(out, QuestStage{c.Quest, c.Value})
		}
	})
	return out
}

func (c Cond) walk(fn func(Cond)) {
	fn(c)
	for _, sub := range c.All {
		sub.walk(fn)
	}
	for _, sub := range c.Any {
		sub.walk(fn)
	}
	if c.Not != nil {
		c.Not.walk(fn)
	}
}

// Ref is what e changes.
func (e Effect) Ref() Ref {
	switch {
	case e.Flag != "":
		return Ref{KindFlag, e.Flag}
	case e.Skill != "":
		return Ref{KindSkill, e.Skill}
	case e.Approval != "":
		return Ref{KindCharacter, e.Approval}
	default:
		return Ref{KindQuest, e.Quest}
	}
}

// World is the state a condition reads.
type World interface {
	Flag(id string) int
	Skill(id string) int
	Approval(characterID string) int
	Quest(id string) int
	QuestDone(id string) bool
	Done(conversationID string) bool
}

// Eval reports whether c holds in w. The zero Cond holds.
func (c Cond) Eval(w World) bool {
	switch {
	case c.All != nil:
		for _, sub := range c.All {
			if !sub.Eval(w) {
				return false
			}
		}
		return true
	case c.Any != nil:
		for _, sub := range c.Any {
			if sub.Eval(w) {
				return true
			}
		}
		return false
	case c.Not != nil:
		return !c.Not.Eval(w)
	case c.Flag != "":
		return compare(w.Flag(c.Flag), c.Op, c.Value)
	case c.Skill != "":
		return compare(w.Skill(c.Skill), c.Op, c.Value)
	case c.Approval != "":
		return compare(w.Approval(c.Approval), c.Op, c.Value)
	case c.Quest != "" && c.Op == "done":
		return w.QuestDone(c.Quest)
	case c.Quest != "":
		return compare(w.Quest(c.Quest), c.Op, c.Value)
	case c.Done != "":
		return w.Done(c.Done)
	}
	return true
}

func compare(v int, op string, n int) bool {
	switch op {
	case "==":
		return v == n
	case "!=", "":
		// A bare flag ("met_mayor") is stored as != 0.
		return v != n
	case ">=":
		return v >= n
	case "<=":
		return v <= n
	case ">":
		return v > n
	case "<":
		return v < n
	}
	return false
}

// Apply is the new value of something at v after e.
func (e Effect) Apply(v int) int {
	switch e.Op {
	case "+=":
		return v + e.Value
	case "-=":
		return v - e.Value
	default:
		return e.Value
	}
}

// ---- names ------------------------------------------------------------------

// Names maps keys to ids and back, for each kind.
type Names struct {
	ids  map[string]map[string]string // kind → key → id
	keys map[string]map[string]string // kind → id → key
}

// NewNames is an empty Names.
func NewNames() *Names {
	return &Names{ids: map[string]map[string]string{}, keys: map[string]map[string]string{}}
}

// Add records that the kind's key names id.
func (n *Names) Add(kind, id, key string) {
	if n.ids[kind] == nil {
		n.ids[kind], n.keys[kind] = map[string]string{}, map[string]string{}
	}
	n.ids[kind][key], n.keys[kind][id] = id, key
}

// Key is the key of a kind's id, or "?id" for one that no longer exists, so
// a script that refers to something deleted still formats and the build can
// point at it.
func (n *Names) Key(kind, id string) string {
	if k, ok := n.keys[kind][id]; ok {
		return k
	}
	return "?" + id
}

func (n *Names) id(kind, key string) (string, error) {
	if id, ok := n.ids[kind][key]; ok {
		return id, nil
	}
	if strings.HasPrefix(key, "?") {
		return "", fmt.Errorf("%s %s was deleted", kind, strings.TrimPrefix(key, "?"))
	}
	known := make([]string, 0, len(n.ids[kind]))
	for k := range n.ids[kind] {
		known = append(known, k)
	}
	slices.Sort(known)
	if len(known) == 0 {
		return "", fmt.Errorf("no %s named %q, and there are no %ss yet", kind, key, kind)
	}
	if len(known) > 12 {
		known = append(known[:12], "…")
	}
	return "", fmt.Errorf("no %s named %q (there is %s)", kind, key, strings.Join(known, ", "))
}

// ---- keys -------------------------------------------------------------------

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,59}$`)

var keywords = []string{"and", "or", "not", "skill", "approval", "quest", "done", "true", "false"}

// CheckKey reports whether key can name something in a script: lowercase
// letters, digits and underscores, starting with a letter, and not a word
// the language uses.
func CheckKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("key %q must be lowercase letters, digits and underscores, starting with a letter, up to 60 characters", key)
	}
	if slices.Contains(keywords, key) {
		return fmt.Errorf("key %q is a word scripts use; pick another", key)
	}
	return nil
}

// Slug turns a name into a likely key: "Aria Venn" → "aria_venn".
func Slug(name string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')
			under = true
		}
	}
	s := strings.TrimRight(b.String(), "_")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "k_" + s
	}
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "_")
	}
	if slices.Contains(keywords, s) {
		s += "_"
	}
	return s
}

// ---- lexing -----------------------------------------------------------------

type token struct {
	kind string // "word", "int", "op", "(", ")", ";", "end"
	text string
	pos  int // 1-based column
}

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '\n' || c == ';':
			out = append(out, token{";", ";", i + 1})
			i++
		case c == '(' || c == ')':
			out = append(out, token{string(c), string(c), i + 1})
			i++
		case c >= '0' && c <= '9' || c == '-' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9' && !lastIsValue(out):
			j := i + 1
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			out = append(out, token{"int", src[i:j], i + 1})
			i = j
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '?':
			j := i + 1
			for j < len(src) && (src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9' || src[j] == '_') {
				j++
			}
			out = append(out, token{"word", strings.ToLower(src[i:j]), i + 1})
			i = j
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch {
			case slices.Contains([]string{"==", "!=", ">=", "<=", "+=", "-="}, two):
				out = append(out, token{"op", two, i + 1})
				i += 2
			case c == '>' || c == '<' || c == '=':
				out = append(out, token{"op", string(c), i + 1})
				i++
			default:
				return nil, fmt.Errorf("unexpected %q at column %d", string(c), i+1)
			}
		}
	}
	return append(out, token{"end", "", len(src) + 1}), nil
}

// lastIsValue says whether a "-" follows a number, where it cannot be a
// sign.
func lastIsValue(ts []token) bool {
	return len(ts) > 0 && (ts[len(ts)-1].kind == "int")
}

type parser struct {
	ts    []token
	i     int
	names *Names
}

func (p *parser) peek() token { return p.ts[p.i] }

func (p *parser) next() token {
	t := p.ts[p.i]
	if t.kind != "end" {
		p.i++
	}
	return t
}

func (p *parser) errorf(t token, format string, args ...any) error {
	where := fmt.Sprintf("at column %d", t.pos)
	if t.kind == "end" {
		where = "at the end"
	}
	return fmt.Errorf(format+" "+where, args...)
}

func (p *parser) word(what string) (token, error) {
	t := p.next()
	if t.kind != "word" || slices.Contains(keywords, t.text) {
		return t, p.errorf(t, "expected %s, found %s", what, describe(t))
	}
	return t, nil
}

func (p *parser) ref(kind string) (string, error) {
	t, err := p.word("a " + kind + " key")
	if err != nil {
		return "", err
	}
	id, err := p.names.id(kind, t.text)
	if err != nil {
		return "", p.errorf(t, "%v", err)
	}
	return id, nil
}

// number reads an integer, or true (1) or false (0).
func (p *parser) number() (int, error) {
	t := p.next()
	switch {
	case t.kind == "int":
		return strconv.Atoi(t.text)
	case t.kind == "word" && t.text == "true":
		return 1, nil
	case t.kind == "word" && t.text == "false":
		return 0, nil
	}
	return 0, p.errorf(t, "expected a number, found %s", describe(t))
}

func describe(t token) string {
	if t.kind == "end" {
		return "nothing"
	}
	return fmt.Sprintf("%q", t.text)
}

// ---- conditions -------------------------------------------------------------

var comparisons = []string{"==", "!=", ">=", "<=", ">", "<"}

// ParseCond parses a condition. Empty text is no condition (nil).
func ParseCond(src string, names *Names) (*Cond, error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	ts, err := lex(src)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.kind == ";" {
			return nil, fmt.Errorf("a condition is one expression; join parts with and or or (column %d)", t.pos)
		}
	}
	p := &parser{ts: ts, names: names}
	c, err := p.or()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != "end" {
		return nil, p.errorf(t, "expected and, or or the end, found %s", describe(t))
	}
	return &c, nil
}

func (p *parser) or() (Cond, error) {
	first, err := p.and()
	if err != nil {
		return Cond{}, err
	}
	parts := []Cond{first}
	for p.peek().kind == "word" && p.peek().text == "or" {
		p.next()
		c, err := p.and()
		if err != nil {
			return Cond{}, err
		}
		if c.Any != nil {
			parts = append(parts, c.Any...)
		} else {
			parts = append(parts, c)
		}
	}
	if len(parts) == 1 {
		return first, nil
	}
	return Cond{Any: parts}, nil
}

func (p *parser) and() (Cond, error) {
	first, err := p.unary()
	if err != nil {
		return Cond{}, err
	}
	parts := []Cond{first}
	if first.All != nil {
		parts = first.All
	}
	for p.peek().kind == "word" && p.peek().text == "and" {
		p.next()
		c, err := p.unary()
		if err != nil {
			return Cond{}, err
		}
		if c.All != nil {
			parts = append(parts, c.All...)
		} else {
			parts = append(parts, c)
		}
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return Cond{All: parts}, nil
}

func (p *parser) unary() (Cond, error) {
	t := p.peek()
	switch {
	case t.kind == "word" && t.text == "not":
		p.next()
		c, err := p.unary()
		if err != nil {
			return Cond{}, err
		}
		return Cond{Not: &c}, nil
	case t.kind == "(":
		p.next()
		c, err := p.or()
		if err != nil {
			return Cond{}, err
		}
		if t := p.next(); t.kind != ")" {
			return Cond{}, p.errorf(t, "expected ), found %s", describe(t))
		}
		return c, nil
	}
	return p.atom()
}

func (p *parser) atom() (Cond, error) {
	t := p.peek()
	if t.kind != "word" {
		return Cond{}, p.errorf(t, "expected a condition, found %s", describe(t))
	}
	switch t.text {
	case "skill", "approval":
		p.next()
		kind := KindSkill
		if t.text == "approval" {
			kind = KindCharacter
		}
		id, err := p.ref(kind)
		if err != nil {
			return Cond{}, err
		}
		op, n, err := p.comparison()
		if err != nil {
			return Cond{}, err
		}
		if t.text == "skill" {
			return Cond{Skill: id, Op: op, Value: n}, nil
		}
		return Cond{Approval: id, Op: op, Value: n}, nil
	case "quest":
		p.next()
		id, err := p.ref(KindQuest)
		if err != nil {
			return Cond{}, err
		}
		if w := p.peek(); w.kind == "word" && w.text == "done" {
			p.next()
			return Cond{Quest: id, Op: "done"}, nil
		}
		op, n, err := p.comparison()
		if err != nil {
			return Cond{}, err
		}
		return Cond{Quest: id, Op: op, Value: n}, nil
	case "done":
		p.next()
		id, err := p.ref(KindConversation)
		if err != nil {
			return Cond{}, err
		}
		return Cond{Done: id}, nil
	}
	id, err := p.ref(KindFlag)
	if err != nil {
		return Cond{}, err
	}
	if o := p.peek(); o.kind == "op" {
		op, n, err := p.comparison()
		if err != nil {
			return Cond{}, err
		}
		return Cond{Flag: id, Op: op, Value: n}, nil
	}
	return Cond{Flag: id, Op: "!=", Value: 0}, nil
}

func (p *parser) comparison() (string, int, error) {
	t := p.next()
	if t.kind != "op" || !slices.Contains(comparisons, t.text) {
		hint := ""
		if t.text == "=" {
			hint = " (use == to compare)"
		}
		return "", 0, p.errorf(t, "expected a comparison (== != >= <= > <), found %s%s", describe(t), hint)
	}
	n, err := p.number()
	return t.text, n, err
}

// FormatCond is c as an author would type it, with today's keys. A nil
// condition is "".
func FormatCond(c *Cond, names *Names) string {
	if c == nil {
		return ""
	}
	return c.format(names, 0)
}

// format writes c; prec is the binding of the context (0 top, 1 inside or,
// 2 inside and, 3 inside not), so groups get parentheses only when needed.
func (c Cond) format(n *Names, prec int) string {
	group := func(parts []Cond, sep string, own int) string {
		s := make([]string, len(parts))
		for i, sub := range parts {
			s[i] = sub.format(n, own)
		}
		out := strings.Join(s, " "+sep+" ")
		if prec > own {
			return "(" + out + ")"
		}
		return out
	}
	switch {
	case c.All != nil:
		return group(c.All, "and", 2)
	case c.Any != nil:
		return group(c.Any, "or", 1)
	case c.Not != nil:
		return "not " + c.Not.format(n, 3)
	case c.Flag != "":
		key := n.Key(KindFlag, c.Flag)
		if c.Op == "!=" && c.Value == 0 {
			return key
		}
		return fmt.Sprintf("%s %s %d", key, c.Op, c.Value)
	case c.Skill != "":
		return fmt.Sprintf("skill %s %s %d", n.Key(KindSkill, c.Skill), c.Op, c.Value)
	case c.Approval != "":
		return fmt.Sprintf("approval %s %s %d", n.Key(KindCharacter, c.Approval), c.Op, c.Value)
	case c.Quest != "" && c.Op == "done":
		return fmt.Sprintf("quest %s done", n.Key(KindQuest, c.Quest))
	case c.Quest != "":
		return fmt.Sprintf("quest %s %s %d", n.Key(KindQuest, c.Quest), c.Op, c.Value)
	case c.Done != "":
		return "done " + n.Key(KindConversation, c.Done)
	}
	return ""
}

// ---- effects ----------------------------------------------------------------

// ParseEffects parses effects separated by ";" or new lines. Empty text is
// no effects.
func ParseEffects(src string, names *Names) ([]Effect, error) {
	ts, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{ts: ts, names: names}
	out := []Effect{}
	for {
		for p.peek().kind == ";" {
			p.next()
		}
		if p.peek().kind == "end" {
			return out, nil
		}
		e, err := p.effect()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		if t := p.peek(); t.kind != ";" && t.kind != "end" {
			return nil, p.errorf(t, "expected ; or a new line between effects, found %s", describe(t))
		}
	}
}

func (p *parser) effect() (Effect, error) {
	t := p.peek()
	if t.kind != "word" {
		return Effect{}, p.errorf(t, "expected an effect, found %s", describe(t))
	}
	switch t.text {
	case "skill", "approval":
		p.next()
		kind := KindSkill
		if t.text == "approval" {
			kind = KindCharacter
		}
		id, err := p.ref(kind)
		if err != nil {
			return Effect{}, err
		}
		op, n, err := p.assignment(true)
		if err != nil {
			return Effect{}, err
		}
		if t.text == "skill" {
			return Effect{Skill: id, Op: op, Value: n}, nil
		}
		return Effect{Approval: id, Op: op, Value: n}, nil
	case "quest":
		p.next()
		id, err := p.ref(KindQuest)
		if err != nil {
			return Effect{}, err
		}
		_, n, err := p.assignment(false)
		if err != nil {
			return Effect{}, err
		}
		if n < 0 {
			return Effect{}, fmt.Errorf("a quest's stage is 0 or more, not %d", n)
		}
		return Effect{Quest: id, Op: "=", Value: n}, nil
	}
	id, err := p.ref(KindFlag)
	if err != nil {
		return Effect{}, err
	}
	if o := p.peek(); o.kind != "op" {
		return Effect{Flag: id, Op: "=", Value: 1}, nil
	}
	op, n, err := p.assignment(true)
	if err != nil {
		return Effect{}, err
	}
	return Effect{Flag: id, Op: op, Value: n}, nil
}

func (p *parser) assignment(arith bool) (string, int, error) {
	t := p.next()
	ok := t.kind == "op" && (t.text == "=" || arith && (t.text == "+=" || t.text == "-="))
	if !ok {
		want := "="
		if arith {
			want = "=, += or -="
		}
		return "", 0, p.errorf(t, "expected %s, found %s", want, describe(t))
	}
	n, err := p.number()
	return t.text, n, err
}

// FormatEffects is es as an author would type them, one per line.
func FormatEffects(es []Effect, names *Names) string {
	lines := make([]string, len(es))
	for i, e := range es {
		switch {
		case e.Flag != "":
			key := names.Key(KindFlag, e.Flag)
			if e.Op == "=" && e.Value == 1 {
				lines[i] = key
			} else {
				lines[i] = fmt.Sprintf("%s %s %d", key, e.Op, e.Value)
			}
		case e.Skill != "":
			lines[i] = fmt.Sprintf("skill %s %s %d", names.Key(KindSkill, e.Skill), e.Op, e.Value)
		case e.Approval != "":
			lines[i] = fmt.Sprintf("approval %s %s %d", names.Key(KindCharacter, e.Approval), e.Op, e.Value)
		default:
			lines[i] = fmt.Sprintf("quest %s = %d", names.Key(KindQuest, e.Quest), e.Value)
		}
	}
	return strings.Join(lines, "\n")
}
