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

// state reports whether the character is still warming up, actively cycling, or
// already settled on its final rune for the current animation frame.
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

// randomize picks a new random rune for the character to display during the
// cycling state, using the shared RNG guarded by a mutex.
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
	ellipsisFrames []string
	ellipsisIdx    int
	lastWidth      int
}

// newLoader constructs a loader with randomized character delays, primes the
// animation so the label appears immediately, and returns the initialized
// instance ready for rendering.
func newLoader() *loader {
	makeDelay := func(max int32, scale time.Duration) time.Duration {
		return time.Duration(rand.Int31n(max)) * scale //nolint:gosec
	}

	makeInitialDelay := func() time.Duration {
		return makeDelay(3, 40*time.Millisecond)
	}

	cycling := make([]loaderChar, loaderCharCyclingCount)
	for i := range cycling {
		cycling[i] = loaderChar{
			finalValue:   -1,
			initialDelay: makeInitialDelay(),
		}
	}

	now := time.Now()
	l := &loader{
		start:          now.Add(-loaderInitialBoost),
		displayStart:   now,
		active:         true,
		minVisible:     350 * time.Millisecond,
		cyclingChars:   cycling,
		ellipsisFrames: []string{"", ".", "..", "..."},
	}
	l.update()
	return l
}

// requestStop signals that the loader should wind down; it stays visible until
// the minimum on-screen duration elapses so the UI does not flicker.
func (l *loader) requestStop() {
	l.shouldStop = true
}

// update advances every animated character according to its timing state and
// deactivates the loader once it has been visible long enough after a stop
// request.
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

	if l.shouldStop && time.Since(l.displayStart) >= l.minVisible {
		l.active = false
	}
}

// advanceEllipsis rotates through the precomputed ellipsis frames, giving the
// loader label a subtle trailing animation while it remains active.
func (l *loader) advanceEllipsis() {
	if !l.active {
		return
	}
	l.ellipsisIdx = (l.ellipsisIdx + 1) % len(l.ellipsisFrames)
}

// View renders the loader into a single string by concatenating the randomised
// glyphs and the current ellipsis frame, padding with spaces when the width
// shrinks so the terminal output remains stable.
func (l *loader) View() string {
	var random strings.Builder
	for _, c := range l.cyclingChars {
		if c.currentValue == 0 {
			continue
		}
		random.WriteRune(c.currentValue)
	}

	randomText := strings.TrimSpace(random.String())
	if randomText == "" {
		randomText = strings.Repeat(".", loaderCharCyclingCount/2)
	}
	text := randomText + " " + l.ellipsisFrames[l.ellipsisIdx]

	width := len([]rune(text))
	if width < l.lastWidth {
		text += strings.Repeat(" ", l.lastWidth-width)
	} else {
		l.lastWidth = width
	}

	return text
}
