package markdown

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// StreamOptions configures how streaming markdown should be rendered.
type StreamOptions struct {
	Raw      bool
	WordWrap int
	Cancel   func()
	UIWriter io.Writer
}

// Chunk represents an incremental markdown fragment emitted by the stream.
type Chunk struct {
	Text string
}

// StreamMarkdown renders streaming markdown to the supplied writer. When Raw is
// false it spins up a Bubble Tea viewport so the output remains scrollable and
// responsive.
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

	return streamWithViewport(ctx, chunkCtx, next, w, rend, cancel, opts.Cancel, opts)
}

// streamRaw simply writes chunks as they arrive without any terminal UI.
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

// streamWithViewport wires the chunk source into the Bubble Tea viewport model.
func streamWithViewport(
	ctx context.Context,
	chunkCtx context.Context,
	next func(context.Context) (Chunk, error),
	w io.Writer,
	rend *glamour.TermRenderer,
	cancel func(),
	onInterrupt func(),
	opts StreamOptions,
) error {
	model := newMarkdownModel(rend, func() tea.Cmd {
		return waitForChunk(chunkCtx, next)
	}, cancel, onInterrupt)

	uiWriter := opts.UIWriter
	if uiWriter == nil {
		uiWriter = w
	}

	prog := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithOutput(uiWriter),
	)

	if _, err := prog.Run(); err != nil {
		return err
	}

	if model.err != nil {
		return model.err
	}

	clearViewport(uiWriter, model.lastView)

	if rendered := model.rendered; rendered != "" {
		if !strings.HasSuffix(rendered, "\n") {
			rendered += "\n"
		}
		if _, err := fmt.Fprintf(w, "%s", rendered); err != nil {
			return err
		}
	}

	return nil
}

// newTermRenderer builds a Glamour renderer honoring the supplied options.
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

// waitForChunk blocks until the next chunk arrives or the context is canceled.
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
	lastView     string
}

// newMarkdownModel constructs the Bubble Tea model that manages the loader and
// viewport rendering.
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

// Init starts the loader animation and schedules the first chunk fetch.
func (m *markdownModel) Init() tea.Cmd {
	return tea.Batch(
		m.loaderStepCmd(),
		m.ellipsisTickCmd(),
		tea.Tick(loaderWarmupDelay, func(time.Time) tea.Msg { return startStreamMsg{} }),
	)
}

// Update processes Bubble Tea messages, wiring streamed chunks into the
// viewport or handling loader/terminal events.
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

// View renders either the loader animation or the markdown viewport.
func (m *markdownModel) View() string {
	if m.loader.active {
		m.lastView = m.loader.View()
		return m.lastView
	}
	m.lastView = m.viewport.View()
	return m.lastView
}

// next requests the next chunk if the producer command is set.
func (m *markdownModel) next() tea.Cmd {
	if m.nextChunk == nil {
		return nil
	}
	return m.nextChunk()
}

// appendChunk adds text to the buffer, re-renders markdown, and scrolls.
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

// resizeViewport adapts the viewport height to fit either the window or the
// current content.
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

// contentLineCount returns the number of lines currently rendered.
func (m *markdownModel) contentLineCount() int {
	if m.rendered == "" {
		return 0
	}
	return strings.Count(m.rendered, "\n")
}

// loaderStepCmd schedules the next loader animation tick when active.
func (m *markdownModel) loaderStepCmd() tea.Cmd {
	if m.loader == nil || !m.loader.active {
		return nil
	}
	return tea.Tick(loaderStepInterval, func(time.Time) tea.Msg {
		return loaderStepMsg{}
	})
}

// ellipsisTickCmd schedules the loader ellipsis animation when active.
func (m *markdownModel) ellipsisTickCmd() tea.Cmd {
	if m.loader == nil || !m.loader.active {
		return nil
	}
	return tea.Tick(loaderEllipsisInterval, func(time.Time) tea.Msg {
		return ellipsisTickMsg{}
	})
}

// clearViewport erases the last viewport rendering from the UI writer so the
// final Glamour output can stand alone once streaming completes.
func clearViewport(w io.Writer, view string) {
	if w == nil || view == "" {
		return
	}

	lines := strings.Count(view, "\n")
	if !strings.HasSuffix(view, "\n") {
		lines++
	}
	if lines < 1 {
		lines = 1
	}

	for i := 0; i < lines; i++ {
		_, _ = fmt.Fprint(w, "\r\033[2K")
		if i+1 < lines {
			_, _ = fmt.Fprint(w, "\033[1A")
		}
	}
}
