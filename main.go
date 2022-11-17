package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"io"
	"io/fs"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	kb = 1024
	mb = 1024 * kb
	gb = 1024 * mb
)

var noCheckMd5 = false
var noLives = false
var dropLess = ""
var dropThreshold = int64(-1)

type fileStatus int

const (
	md5passed fileStatus = iota
	md5failed
	inProgress
)

type lastFileItem struct {
	path   string
	name   string
	status fileStatus
}

type uiState int

const (
	uiStateSearching uiState = iota
	uiStateUploading
)
const fileHistoryLines = 5

var appPadding = lipgloss.NewStyle().Padding(1, 2, 1, 2).Render
var greyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render
var inProgressStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#fdfdfd")).Bold(true).Render
var passedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Render
var failedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render

func getMd5(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "failed" // FIXME add error handling
	}
	defer file.Close()
	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "failed"
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func parseSize(s string) (int64, error) {
	idx := strings.LastIndexAny(s, "0123456789.")
	if idx == -1 {
		return 0, fmt.Errorf("размер должен быть в формате 123.45G")
	}
	sizeStr := s[:idx+1]
	unit := strings.ToLower(s[idx+1:])
	size, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return 0, err
	}
	if size <= 0 {
		return 0, fmt.Errorf("размер должен быть больше нуля")
	}
	multiplier := 0
	switch unit {
	case "g", "gb", "гб":
		multiplier = gb
	case "m", "mb", "мб":
		multiplier = mb
	case "k", "kb", "кб":
		multiplier = kb
	case "":
		multiplier = 1
	default:
		return 0, fmt.Errorf("размер должен быть в формате 123.45G")
	}

	return int64(size * float64(multiplier)), nil
}

func formatSize(s int64) string {
	if s >= gb {
		return fmt.Sprintf("%.1fГб", float64(s)/float64(gb))
	} else if s >= mb {
		return fmt.Sprintf("%.1fМб", float64(s)/float64(mb))
	} else if s >= kb {
		return fmt.Sprintf("%.1fКб", float64(s)/float64(kb))
	} else {
		return fmt.Sprintf("%d байт", s)
	}
}

type copyWatcher struct {
	bytes int64
	ch    chan fillerMsg
}

func (w *copyWatcher) Write(p []byte) (int, error) {
	diff := int64(len(p))
	w.bytes += diff
	w.ch <- bytesCopied{diff: diff, total: w.bytes}
	return len(p), nil
}

func copyFile(from, to string, ch chan fillerMsg) error {
	r, err := os.Open(from)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(to)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, io.TeeReader(r, &copyWatcher{
		bytes: 0,
		ch:    ch,
	}))
	if err != nil {
		return err
	}
	return nil
}

var pattern = "mp3"

func getPatterns(source string) []string {
	patterns := strings.Split(source, ",")
	for i := 0; i < len(patterns); i++ {
		x := patterns[i]
		x = strings.TrimSpace(x)
		if !strings.HasPrefix(x, ".") {
			x = "." + x
		}
		patterns[i] = x

	}
	return patterns
}

var livePattern = regexp.MustCompile("\\blive\\b")

func matchesPatterns(patterns []string, noLives bool, path string) bool {
	if noLives {
		dir, filename := filepath.Split(path)
		parentDir := filepath.Base(dir)
		if livePattern.MatchString(strings.ToLower(filename)) || livePattern.MatchString(strings.ToLower(parentDir)) {
			return false
		}
	}
	if len(patterns) == 0 {
		return true
	}

	ext := strings.ToLower(filepath.Ext(path))
	for _, p := range patterns {
		if ext == p {
			return true
		}
	}

	return false
}

func (f fileStatus) Style(s string) string {
	switch f {
	case inProgress:
		return inProgressStyle("> " + s)
	case md5passed:
		return passedStyle("  " + s)
	case md5failed:
		return failedStyle("  " + s)
	}
	return s
}

type model struct {
	sub chan fillerMsg
	// All states
	currentState uiState
	start        time.Time
	explanation  string
	// Searching State
	filesFound    int
	filesMatch    int
	spinner       spinner.Model
	lastFileFound string
	// UploadingState
	uploadStartedAt        time.Time
	totalProgress          progress.Model
	currentProgress        progress.Model
	overallBytes           int64
	overallFiles           int
	overallBytesCopied     int64
	currentFileBytesCopied int64
	currentFileBytes       int64
	currentFilename        string
	currentFiles           int
	lastFiles              []*lastFileItem
	failedMd5Number        int
}

func newApp(sub chan fillerMsg, explanation string) model {
	return model{
		sub:                    sub,
		currentState:           uiStateSearching,
		start:                  time.Now(),
		explanation:            explanation,
		filesFound:             0,
		filesMatch:             0,
		spinner:                spinner.New(spinner.WithSpinner(spinner.Dot)),
		lastFileFound:          "",
		totalProgress:          progress.New(progress.WithDefaultGradient()),
		currentProgress:        progress.New(progress.WithDefaultGradient()),
		overallBytes:           0,
		overallFiles:           0,
		overallBytesCopied:     0,
		currentFileBytesCopied: int64(0),
		currentFileBytes:       0,
		currentFilename:        "",
		currentFiles:           0,
		lastFiles:              make([]*lastFileItem, 0, fileHistoryLines+1),
		failedMd5Number:        0,
	}
}

type fillerMsg any

func activityBridge(ch chan fillerMsg) tea.Cmd {
	return func() tea.Msg {
		msg := <-ch
		return msg
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(activityBridge(m.sub), m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case done:
		return m, tea.Quit
	case totalsFound:
		m.overallFiles = msg.files
		m.overallBytes = msg.bytes
	case newFileStarted:
		m.lastFiles = append(m.lastFiles, &lastFileItem{
			name:   msg.currentFilename,
			status: inProgress,
			path:   msg.path,
		})
		if len(m.lastFiles) == fileHistoryLines+1 {
			m.lastFiles = m.lastFiles[1:]
		}
		m.currentFilename = msg.currentFilename
		m.currentFileBytes = msg.currentFileBytes
		m.currentFiles = msg.currentFileNumber
	case bytesCopied:
		m.currentFileBytesCopied = msg.total
		m.overallBytesCopied += msg.diff
	case md5checked:
		for _, lf := range m.lastFiles {
			if lf.path == msg.path {
				lf.status = msg.status
			}
		}
		if msg.status == md5failed {
			m.failedMd5Number++
		}
	case uiState:
		m.currentState = msg
		switch msg {
		case uiStateUploading:
			m.uploadStartedAt = time.Now()
		}
	case searchFileFound:
		m.filesFound++
		m.lastFileFound = string(msg)
	case searchFileMatches:
		m.filesMatch++
	}

	// we do not really need these spinner ticks once Searching State is finished,
	// but they are really handy to make the event loop spin and show realtime updates
	// to the user console
	newSpinner, cmd := m.spinner.Update(msg)
	m.spinner = newSpinner

	return m, tea.Batch(activityBridge(m.sub), cmd)
}

func (m model) viewState() string {
	switch m.currentState {
	case uiStateSearching:
		return fmt.Sprintf(
			"\n\n%s\n%s %d файлов найдено (%d подходит)\n\n",
			m.lastFileFound,
			m.spinner.View(),
			m.filesFound,
			m.filesMatch,
		)
	case uiStateUploading:
		overallProgress := float64(m.overallBytesCopied) / float64(m.overallBytes)
		currentFileProgress := float64(m.currentFileBytesCopied) / float64(m.currentFileBytes)
		speed := float64(m.overallBytesCopied) / time.Since(m.start).Seconds()

		lastFilesLines := make([]string, 0, len(m.lastFiles))
		for i := len(m.lastFiles) - 1; i >= 0; i-- {
			lf := m.lastFiles[i]
			lastFilesLines = append(lastFilesLines, lf.status.Style(lf.name))
		}

		for i := 0; i < fileHistoryLines-len(m.lastFiles); i++ {
			lastFilesLines = append(lastFilesLines, "")
		}

		failedMsg := ""
		if m.failedMd5Number != 0 {
			failedMsg = " / " + failedStyle(fmt.Sprintf("%d ошибок md5", m.failedMd5Number))
		}

		lastFilesFmt := strings.Join(lastFilesLines, "\n")
		return fmt.Sprintf(
			"Прогресс: %d / %d%s [%s/сек]\n%s\nТекущий файл: %s [%s]\n%s\n\n%s\n\n",
			m.currentFiles,
			m.overallFiles,
			failedMsg,
			formatSize(int64(speed)),
			m.totalProgress.ViewAs(overallProgress),
			m.currentFilename,
			formatSize(m.currentFileBytes),
			m.currentProgress.ViewAs(currentFileProgress),
			lastFilesFmt,
		)
	}
	return "unknown state"
}

var helpText = greyStyle("Нажмите ctrl+c для выхода...")

func (m model) View() string {
	curStateView := m.viewState()
	duration := time.Since(m.start)
	uploadDuration := time.Since(m.uploadStartedAt)
	passed := fmt.Sprintf("Прошло: %s", duration.Round(time.Second))
	leftStr := ""
	if m.overallBytes > 0 && m.overallBytesCopied > 0 {
		overallProgress := float64(m.overallBytesCopied) / float64(m.overallBytes)
		left := time.Duration((uploadDuration.Seconds()/overallProgress)-uploadDuration.Seconds()) * time.Second
		leftStr = fmt.Sprintf(" / Осталось %s", left.Round(time.Second))
	}
	passed += leftStr
	return appPadding(fmt.Sprintf(
		"%s\n%s\n\n%s\n%s",
		greyStyle(m.explanation),
		greyStyle(passed),
		curStateView,
		helpText,
	))
}

type totalsFound struct {
	files int
	bytes int64
}
type newFileStarted struct {
	currentFileNumber int
	currentFilename   string
	path              string
	currentFileBytes  int64
}
type bytesCopied struct {
	diff  int64
	total int64
}
type done struct{}
type md5checked struct {
	path   string
	status fileStatus
}
type searchFileFound string
type searchFileMatches struct{}

var noGUI bool

func main() {
	rand.Seed(time.Now().UnixNano())
	flag.StringVar(&pattern, "pattern", "mp3", "файлы для поиска. Например: -pattern=mp3,ogg. По умолчанию: mp3")
	flag.BoolVar(&noCheckMd5, "nomd5", false, "не проверять хэш-суммы после записи")
	flag.BoolVar(&noLives, "nolive", false, "не включать в список live выступления (если в имени файла или родительской папке содержится 'live') [0/1]. По умолчанию: 0")
	flag.StringVar(&dropLess, "drop", "", "не включать в список файлы, размер которых меньше параметра (например: -drop=1M или -drop=900K). По умолчанию включаются все")
	flag.BoolVar(&noGUI, "nogui", false, "не отображать GUI, вместо этого писать логи")
	flag.Parse()
	args := flag.Args()
	if flag.NArg() != 3 {
		cmd := os.Args[0]
		fmt.Printf("Использование: %s -drop=1M 15G \"D:\\Music\\My Best Collection\" \"E\\\"\nДоступные настройки: %s -h\n", cmd, cmd)
		os.Exit(1)
	}
	sizeStr := args[0]
	from := args[1]
	to := args[2]
	if !noGUI {
		log.SetOutput(io.Discard)
	}
	patterns := getPatterns(pattern)
	left, err := parseSize(sizeStr)
	if err != nil {
		log.Fatalln(err)
	}
	if dropLess != "" {
		dropThreshold, err = parseSize(dropLess)
		if err != nil {
			log.Fatalln(err)
		}
	}
	sub := make(chan fillerMsg, 10)
	_makeExplanation := func() string {
		parts := make([]string, 0)
		parts = append(parts, fmt.Sprintf("Ищем файлы %s в %s", patterns, from))
		parts = append(parts, fmt.Sprintf("пишем %s в %s", formatSize(left), to))
		hashCheck := "проверяя контрольные суммы при записи"
		if noCheckMd5 {
			hashCheck = "не " + hashCheck
		}
		parts = append(parts, hashCheck)
		if dropThreshold != -1 {
			parts = append(parts, fmt.Sprintf("пропуская файлы меньше %s", formatSize(dropThreshold)))
		}
		if noLives {
			parts = append(parts, "пропуская Live файлы")
		}
		return strings.Join(parts, ", ")
	}

	log.Println(_makeExplanation())
	doneCh := make(chan struct{})
	go func() {
		_matchesLimits := func(path string) bool {
			patterned := matchesPatterns(patterns, noLives, path)
			if !patterned {
				return false
			}
			if dropThreshold != -1 {
				info, err := os.Stat(path)
				if err != nil {
					return false
				}
				return info.Size() >= dropThreshold
			}
			return true
		}

		files := make([]string, 0)
		err = filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
			if !d.IsDir() {
				sub <- searchFileFound(path)
			}
			if !d.IsDir() && _matchesLimits(path) {
				files = append(files, path)
				sub <- searchFileMatches{}
			}
			return nil
		})

		sub <- uiStateUploading

		if err != nil {
			log.Fatalln(err)
		}
		log.Println("найдено файлов:", len(files))

		rand.Shuffle(len(files), func(i, j int) {
			files[i], files[j] = files[j], files[i]
		})
		toWrite := make([]string, 0)
		toWriteBytesCount := int64(0)

		tries := 0
		for _, path := range files {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}

			size := info.Size()
			if left >= size {
				toWrite = append(toWrite, path)
				left -= size
				toWriteBytesCount += size
				tries = 0
			} else if tries > 10 {
				break
			} else {
				tries++
			}
		}

		sub <- totalsFound{
			files: len(toWrite),
			bytes: toWriteBytesCount,
		}
		log.Println("будет записано файлов:", len(toWrite), "на", formatSize(toWriteBytesCount))

		counter := 0
		parentCreated := false

		copyError := false
		for i, path := range toWrite {
			info, err := os.Stat(path)
			if err != nil {
				copyError = true
				continue
			}
			_, filename := filepath.Split(path)

			sub <- newFileStarted{
				currentFileNumber: i + 1,
				currentFilename:   filename,
				currentFileBytes:  info.Size(),
				path:              path,
			}
			ext := filepath.Ext(path)
			name := fmt.Sprintf("%010d%s", counter, ext)
			counter++
			newPath := filepath.Join(to, name)
			log.Println("Пишем", path, "->", newPath)
			if !parentCreated {
				parentDir := filepath.Dir(newPath)
				if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
					log.Fatalln("не удалось создать родительские папки")
				}
				parentCreated = true
			}
			err = copyFile(path, newPath, sub)
			if err != nil {
				copyError = true
				log.Println(err.Error())
				continue
			}

			md5event := md5checked{
				path: path,
			}
			// TODO: checking source md5 via TeeReader in copyFile will remove an extra read
			if noCheckMd5 || getMd5(path) == getMd5(newPath) {
				md5event.status = md5passed
			} else {
				md5event.status = md5failed
				log.Printf("md5 %s -> %s не совпали\n", path, newPath)
				copyError = true
			}
			sub <- md5event
		}

		sub <- done{}
		if copyError && noGUI {
			os.Exit(1)
		}
		doneCh <- struct{}{}
	}()
	opts := make([]tea.ProgramOption, 0)
	if noGUI {
		opts = append(opts, tea.WithoutRenderer())
	}
	_, err = tea.NewProgram(newApp(sub, _makeExplanation()), opts...).Run()
	if err != nil {
		log.Fatalln(err)
	}
	<-doneCh
}
