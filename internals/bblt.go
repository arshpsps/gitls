package internals

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-github/v50/github"
	"golang.org/x/oauth2"
)

var (
	normalStyle  = lipgloss.NewStyle().Margin(1, 2)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
)

type item struct {
	name string
	url  string
}

func (i item) Title() string       { return i.name }
func (i item) Description() string { return i.url }
func (i item) FilterValue() string { return i.name }

type repoModel struct {
	username   string
	repos      []*github.Repository
	list       list.Model
	err        error
	spinner    spinner.Model
	cloning    bool
	cloneMsg   string
	cloneError bool
}

type usernameModel struct {
	rootModel repoModel
	username  string
	textInput textinput.Model
	err       error
}

func prepUsernameModel(username string, rootModel repoModel) usernameModel {
	ti := textinput.New()
	ti.SetValue(username)
	ti.Focus()
	ti.Cursor.Focus()
	ti.CharLimit = 64

	return usernameModel{
		rootModel: rootModel,
		username:  username,
		textInput: ti,
		err:       nil,
	}
}

func (m usernameModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m usernameModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyEnter:
			username := strings.TrimSpace(m.textInput.Value())
			if username == "" {
				return m, nil
			}
			return initialModel(username), nil

		case tea.KeyEsc:
			if m.username == "" {
				return m, tea.Quit
			} else {
				return m.rootModel, nil
			}

		}
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m usernameModel) View() string {
	return fmt.Sprintf(
		"What’s your Github Username?\n%s\n\n%s",
		m.textInput.View(),
		"(esc to quit)",
	) + "\n"
}

type cloneFinishedMsg struct {
	err error
	dir string
}

func cloneRepo(url string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("git", "clone", url)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return cloneFinishedMsg{
				err: fmt.Errorf("%w: %s", err, string(output)),
				dir: "",
			}
		}
		return cloneFinishedMsg{
			err: nil,
			dir: url[strings.LastIndex(url, "/")+1 : len(url)-4], // crazy url parsing
		}
	}
}

func (m repoModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m repoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" && !m.cloning {
			return m, tea.Quit
		}
		if msg.String() == "enter" && !m.cloning {
			selectedItem := m.list.SelectedItem().(item)
			m.cloning = true
			m.cloneMsg = fmt.Sprintf("Cloning %s...", selectedItem.name)
			return m, tea.Batch(
				m.spinner.Tick,
				cloneRepo(selectedItem.url),
			)
		}
		if msg.String() == "c" && !m.cloning {
			return prepUsernameModel(m.username, m), nil
		}
	case tea.WindowSizeMsg:
		h, v := normalStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
	case cloneFinishedMsg:
		m.cloning = false
		if msg.err != nil {
			m.cloneError = true
			m.cloneMsg = fmt.Sprintf("Error cloning: %v", msg.err)
		} else {
			m.cloneError = false
			m.cloneMsg = fmt.Sprintf("Successfully cloned to %s/", msg.dir)
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m repoModel) View() string {
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error fetching repos: %v\nPress any key to exit", m.err))
	}

	if m.cloning {
		return normalStyle.Render(
			lipgloss.JoinVertical(
				lipgloss.Left,
				m.list.View(),
				"\n"+m.spinner.View()+" "+m.cloneMsg,
			),
		)
	}

	if m.cloneMsg != "" {
		style := successStyle
		if m.cloneError {
			style = errorStyle
		}
		return normalStyle.Render(
			lipgloss.JoinVertical(
				lipgloss.Left,
				m.list.View(),
				"\n"+style.Render(m.cloneMsg),
			),
		)
	}

	return normalStyle.Render(m.list.View())
}

func initialModel(username string) tea.Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	repos, err := fetchRepos(username)
	if err != nil {
		return repoModel{
			username: username,
			err:      err,
			spinner:  sp,
			list:     list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		}
	}

	if len(repos) <= 0 {
		return repoModel{
			username: username,
			err:      err,
			spinner:  sp,
			list:     list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		}
	}

	items := make([]list.Item, len(repos))
	for i, repo := range repos {
		items[i] = item{
			name: repo.GetName(),
			url:  repo.GetCloneURL(),
		}
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = username + "'s GitHub Repositories"

	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "clone repo"),
			),
			key.NewBinding(
				key.WithKeys("c"),
				key.WithHelp("c", "change user"),
			),
		}
	}

	l.AdditionalFullHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "clone selected repository"),
			),
			key.NewBinding(
				key.WithKeys("c"),
				key.WithHelp("c", "change GitHub username"),
			),
		}
	}

	l.SetSize(80, 24)

	return repoModel{
		username: username,
		repos:    repos,
		list:     l,
		spinner:  sp,
	}
}

func fetchRepos(username string) ([]*github.Repository, error) {
	ctx := context.Background()
	token := os.Getenv("GITHUB_TOKEN")

	var client *github.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		client = github.NewClient(oauth2.NewClient(ctx, ts))
	} else {
		client = github.NewClient(nil)
	}

	opt := &github.RepositoryListOptions{
		Type:        "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var allRepos []*github.Repository
	for {
		repos, resp, err := client.Repositories.List(ctx, username, opt)
		if err != nil {
			return nil, fmt.Errorf("failed to list repos: %w", err)
		}
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return allRepos, nil
}

func BbltRun() {
	var model tea.Model

	cmd := exec.Command("git", "config", "user.name")
	out, err := cmd.CombinedOutput()
	un := strings.TrimSpace(string(out))
	if err != nil && un == "" {
		model = prepUsernameModel("", repoModel{})
	} else {
		model = initialModel(un)
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}
}
