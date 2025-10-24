package markdown

import (
	"context"
	"io"
	"strings"
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
	chunks <-chan Chunk,
	w io.Writer,
	opts StreamOptions,
) error {
	if opts.Raw {
		return streamRaw(ctx, chunks, w)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rend, err := newTermRenderer(opts)
	if err != nil {
		return err
	}

	return streamWithViewport(ctx, chunks, w, rend, cancel, opts.Cancel)
}

func streamRaw(ctx context.Context, chunks <-chan Chunk, w io.Writer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return nil
			}
			if chunk.Text == "" {
				continue
			}
			if _, err := io.WriteString(w, chunk.Text); err != nil {
				return err
			}
		}
	}
}

func streamWithViewport(
	ctx context.Context,
	chunks <-chan Chunk,
	w io.Writer,
	rend *glamour.TermRenderer,
	cancel func(),
	onInterrupt func(),
) error {
	model := newMarkdownModel(rend, func() tea.Cmd {
		return waitForChunk(ctx, chunks)
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

func waitForChunk(ctx context.Context, chunks <-chan Chunk) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return doneMsg{err: ctx.Err()}
		case chunk, ok := <-chunks:
			if !ok {
				return doneMsg{}
			}
			return chunkMsg(chunk.Text)
		}
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
	}
}

func (m *markdownModel) Init() tea.Cmd {
	return m.next()
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
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *markdownModel) View() string {
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
