package markdown

import (
	"math/rand"
	"strings"
	"sync"
	"time"
)

const (
	// loaderCharCyclingCount controls how many random glyphs animate per frame.
	loaderCharCyclingCount = 30
	// loaderStepInterval is the cadence for shuffling random glyphs.
	loaderStepInterval = time.Second / 22
	// loaderEllipsisInterval governs how quickly the trailing ellipsis spins.
	loaderEllipsisInterval = 220 * time.Millisecond
	// loaderInitialBoost seeds the loader as if this much time already elapsed,
	// so the label appears fully formed on the very first frame.
	loaderInitialBoost = 200 * time.Millisecond
	// loaderWarmupDelay waits this long before fetching the first chunk so the
	// loader has at least one frame to render.
	loaderWarmupDelay = 50 * time.Millisecond
)

var (
	loaderRunes  = []rune("0123456789abcdefABCDEF~!@#$%^&*()+=_")
	loaderRand   = rand.New(rand.NewSource(time.Now().UnixNano()))
	loaderRandMu sync.Mutex
)

type loaderState int

const (
	loaderInitial loaderState = iota
	loaderCycling
	loaderSettled
)

type loaderChar struct {
	finalValue   rune
	currentValue rune
	initialDelay time.Duration
	lifetime     time.Duration
}

func (c loaderChar) state(start time.Time) loaderState {
	now := time.Now()
	if now.Before(start.Add(c.initialDelay)) {
		return loaderInitial
	}
	if c.finalValue > 0 && c.lifetime > 0 && now.After(start.Add(c.initialDelay+c.lifetime)) {
		return loaderSettled
	}
	return loaderCycling
}

func (c *loaderChar) randomize() {
	loaderRandMu.Lock()
	idx := loaderRand.Intn(len(loaderRunes))
	loaderRandMu.Unlock()
	c.currentValue = loaderRunes[idx]
}

type loader struct {
	start          time.Time
	displayStart   time.Time
	active         bool
	shouldStop     bool
	minVisible     time.Duration
	cyclingChars   []loaderChar
	labelChars     []loaderChar
	label          []rune
	ellipsisFrames []string
	ellipsisIdx    int
}

func newLoader() *loader {
	label := []rune(" Preparing response")
	makeDelay := func(max int32, scale time.Duration) time.Duration {
		return time.Duration(rand.Int31n(max)) * scale //nolint:gosec
	}

	makeInitialDelay := func() time.Duration {
		return makeDelay(3, 40*time.Millisecond)
	}

	makeLifetime := func() time.Duration {
		return makeDelay(5, 160*time.Millisecond) + 120*time.Millisecond
	}

	cycling := make([]loaderChar, loaderCharCyclingCount)
	for i := range cycling {
		cycling[i] = loaderChar{
			finalValue:   -1,
			initialDelay: makeInitialDelay(),
		}
	}

	labelChars := make([]loaderChar, len(label))
	for i, r := range label {
		labelChars[i] = loaderChar{
			finalValue:   r,
			initialDelay: makeInitialDelay(),
			lifetime:     makeLifetime(),
		}
	}

	now := time.Now()
	l := &loader{
		start:          now.Add(-loaderInitialBoost),
		displayStart:   now,
		active:         true,
		minVisible:     350 * time.Millisecond,
		cyclingChars:   cycling,
		labelChars:     labelChars,
		label:          label,
		ellipsisFrames: []string{"", ".", "..", "..."},
	}
	l.update()
	return l
}

func (l *loader) requestStop() {
	l.shouldStop = true
}

func (l *loader) update() {
	if !l.active {
		return
	}
	for i := range l.cyclingChars {
		switch l.cyclingChars[i].state(l.start) {
		case loaderInitial:
			l.cyclingChars[i].currentValue = '.'
		case loaderCycling:
			l.cyclingChars[i].randomize()
		case loaderSettled:
			l.cyclingChars[i].currentValue = l.cyclingChars[i].finalValue
		}
	}

	for i := range l.labelChars {
		switch l.labelChars[i].state(l.start) {
		case loaderInitial:
			l.labelChars[i].currentValue = '.'
		case loaderCycling:
			l.labelChars[i].randomize()
		case loaderSettled:
			l.labelChars[i].currentValue = l.labelChars[i].finalValue
		}
	}

	if l.shouldStop && time.Since(l.displayStart) >= l.minVisible {
		l.active = false
	}
}

func (l *loader) advanceEllipsis() {
	if !l.active {
		return
	}
	l.ellipsisIdx = (l.ellipsisIdx + 1) % len(l.ellipsisFrames)
}

func (l *loader) View() string {
	var b strings.Builder
	for _, c := range l.cyclingChars {
		if c.currentValue == 0 {
			continue
		}
		b.WriteRune(c.currentValue)
	}
	b.WriteRune(' ')
	for _, c := range l.labelChars {
		if c.currentValue == 0 {
			continue
		}
		b.WriteRune(c.currentValue)
	}
	b.WriteString(l.ellipsisFrames[l.ellipsisIdx])
	return b.String()
}
