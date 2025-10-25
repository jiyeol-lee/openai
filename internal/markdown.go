package markdown

import (
	"context"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

type StreamOptions struct {
	Raw      bool
	WordWrap int
	Cancel   func()
}

type Chunk struct {
	Text string
}

func StreamMarkdown(
	ctx context.Context,
	next func(context.Context) (Chunk, error),
	w io.Writer,
	opts StreamOptions,
) error {
	chunkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if opts.Raw {
		return streamRaw(chunkCtx, next, w)
	}

	rend, err := newTermRenderer(opts)
	if err != nil {
		return err
	}

	return streamWithViewport(ctx, chunkCtx, next, w, rend, cancel, opts.Cancel)
}

func streamRaw(ctx context.Context, next func(context.Context) (Chunk, error), w io.Writer) error {
	for {
		chunk, err := next(ctx)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if chunk.Text == "" {
			continue
		}
		if _, err := io.WriteString(w, chunk.Text); err != nil {
			return err
		}
	}
}

func streamWithViewport(
	ctx context.Context,
	chunkCtx context.Context,
	next func(context.Context) (Chunk, error),
	w io.Writer,
	rend *glamour.TermRenderer,
	cancel func(),
	onInterrupt func(),
) error {
	model := newMarkdownModel(rend, func() tea.Cmd {
		return waitForChunk(chunkCtx, next)
	}, cancel, onInterrupt)

	prog := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithOutput(w),
	)

	if _, err := prog.Run(); err != nil {
		return err
	}

	return model.err
}

func newTermRenderer(opts StreamOptions) (*glamour.TermRenderer, error) {
	wrap := 120
	if opts.WordWrap > 0 {
		wrap = opts.WordWrap
	}
	return glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wrap),
	)
}

type chunkMsg string

type doneMsg struct {
	err error
}

type loaderStepMsg struct{}

type ellipsisTickMsg struct{}

type startStreamMsg struct{}

func waitForChunk(
	ctx context.Context,
	next func(context.Context) (Chunk, error),
) tea.Cmd {
	return func() tea.Msg {
		chunk, err := next(ctx)
		if err == io.EOF {
			return doneMsg{}
		}
		if err != nil {
			return doneMsg{err: err}
		}
		return chunkMsg(chunk.Text)
	}
}

type markdownModel struct {
	renderer     *glamour.TermRenderer
	viewport     viewport.Model
	content      strings.Builder
	rendered     string
	windowWidth  int
	windowHeight int
	nextChunk    func() tea.Cmd
	cancel       func()
	onInterrupt  func()
	err          error
	loader       *loader
}

func newMarkdownModel(
	rend *glamour.TermRenderer,
	next func() tea.Cmd,
	cancel func(),
	onInterrupt func(),
) *markdownModel {
	vp := viewport.New(0, 0)
	vp.GotoBottom()
	return &markdownModel{
		renderer:    rend,
		viewport:    vp,
		nextChunk:   next,
		cancel:      cancel,
		onInterrupt: onInterrupt,
		loader:      newLoader(),
	}
}

func (m *markdownModel) Init() tea.Cmd {
	return tea.Batch(
		m.loaderStepCmd(),
		m.ellipsisTickCmd(),
		tea.Tick(loaderWarmupDelay, func(time.Time) tea.Msg { return startStreamMsg{} }),
	)
}

func (m *markdownModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case chunkMsg:
		if err := m.appendChunk(string(msg)); err != nil {
			m.err = err
			return m, tea.Quit
		}
		return m, m.next()
	case doneMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		m.loader.requestStop()
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			if m.cancel != nil {
				m.cancel()
			}
			if m.onInterrupt != nil {
				m.onInterrupt()
			}
			m.err = context.Canceled
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.windowHeight = msg.Height
		m.viewport.Width = msg.Width
		m.resizeViewport()
		m.viewport.SetContent(m.rendered)
		return m, nil
	case loaderStepMsg:
		if m.loader.active {
			m.loader.update()
			return m, m.loaderStepCmd()
		}
		return m, nil
	case ellipsisTickMsg:
		if m.loader.active {
			m.loader.advanceEllipsis()
			return m, m.ellipsisTickCmd()
		}
		return m, nil
	case startStreamMsg:
		return m, m.next()
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *markdownModel) View() string {
	if m.loader.active {
		return m.loader.View()
	}
	return m.viewport.View()
}

func (m *markdownModel) next() tea.Cmd {
	if m.nextChunk == nil {
		return nil
	}
	return m.nextChunk()
}

func (m *markdownModel) appendChunk(text string) error {
	if text == "" {
		return nil
	}

	if m.loader.active {
		m.loader.requestStop()
	}

	m.content.WriteString(text)

	rendered, err := m.renderer.Render(m.content.String())
	if err != nil {
		return err
	}

	rendered = strings.TrimRightFunc(rendered, unicode.IsSpace) + "\n"
	m.rendered = rendered
	m.resizeViewport()
	m.viewport.SetContent(rendered)
	m.viewport.GotoBottom()

	return nil
}

func (m *markdownModel) resizeViewport() {
	contentHeight := m.contentLineCount()

	height := m.windowHeight
	if height == 0 {
		height = m.viewport.Height
	}

	if contentHeight > 0 && (height == 0 || contentHeight < height) {
		height = contentHeight
	}

	if height < 1 {
		height = 1
	}

	m.viewport.Height = height
}

func (m *markdownModel) contentLineCount() int {
	if m.rendered == "" {
		return 0
	}
	return strings.Count(m.rendered, "\n")
}

func (m *markdownModel) loaderStepCmd() tea.Cmd {
	if m.loader == nil || !m.loader.active {
		return nil
	}
	return tea.Tick(loaderStepInterval, func(time.Time) tea.Msg {
		return loaderStepMsg{}
	})
}

func (m *markdownModel) ellipsisTickCmd() tea.Cmd {
	if m.loader == nil || !m.loader.active {
		return nil
	}
	return tea.Tick(loaderEllipsisInterval, func(time.Time) tea.Msg {
		return ellipsisTickMsg{}
	})
}

const (
	loaderCharCyclingCount = 18
	loaderStepInterval     = time.Second / 22
	loaderEllipsisInterval = 220 * time.Millisecond
	loaderInitialBoost     = 200 * time.Millisecond
	loaderWarmupDelay      = 50 * time.Millisecond
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
	if c.finalValue <= 0 {
		return loaderCycling
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
