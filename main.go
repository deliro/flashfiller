package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/term"
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

func getMd5(path string) string {
	file, err := os.Open(path)

	if err != nil {
		panic(err)
	}

	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)

	if err != nil {
		panic(err)
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
	case "g":
		multiplier = gb
	case "m":
		multiplier = mb
	case "k":
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
		return fmt.Sprintf("%.1fG", float64(s)/float64(gb))
	} else if s >= mb {
		return fmt.Sprintf("%.1fM", float64(s)/float64(mb))
	} else if s >= kb {
		return fmt.Sprintf("%.1fK", float64(s)/float64(kb))
	} else {
		return fmt.Sprintf("%d байт", s)
	}
}

func copyFile(from, to string) error {
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
	_, err = io.Copy(w, r)
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

func formatFilename(s string) string {
	w, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		w = 80
	}
	free := w - 65
	if free < 10 {
		return ""
	}
	if free > 25 {
		free = 20
	}

	if len(s) <= free {
		return s
	}

	return fmt.Sprintf("...%s", s[len(s)-(free-3):])
}

var checkMd5 = true
var noLives = false
var dropLess = ""
var dropThreshold = int64(-1)

func main() {
	rand.Seed(time.Now().UnixNano())
	flag.StringVar(&pattern, "pattern", "mp3", "файлы для поиска. Например: -pattern=mp3,ogg. По умолчанию: mp3")
	flag.BoolVar(&checkMd5, "md5", true, "проверять хэш-суммы после записи [0/1]. По умолчанию: 1")
	flag.BoolVar(&noLives, "nolive", false, "не включать в список live выступления (если в имени файла или родительской папке содержится 'live') [0/1]. По умолчанию: 0")
	flag.StringVar(&dropLess, "drop", "", "не включать в список файлы, размер которых меньше параметра (например: -drop=1M или -drop=900K). По умолчанию включаются все")
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

	_makeExplanation := func() string {
		parts := make([]string, 0)
		parts = append(parts, fmt.Sprintf("Ищем файлы %s в %s", patterns, from))
		parts = append(parts, fmt.Sprintf("пишем %s в %s", formatSize(left), to))
		hashCheck := "проверяя контрольные суммы при записи"
		if !checkMd5 {
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

	walkPg := progressbar.NewOptions(-1,
		progressbar.OptionSpinnerType(14),
		progressbar.OptionSetElapsedTime(true),
		progressbar.OptionShowCount(),
		progressbar.OptionThrottle(time.Millisecond*100),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetItsString("files"),
	)

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
		if !d.IsDir() && _matchesLimits(path) {
			files = append(files, path)
			walkPg.Add(1)
			_, filename := filepath.Split(path)
			walkPg.Describe(formatFilename(filename))
		}
		return nil
	})

	if err != nil {
		log.Fatalln(err)
	}
	walkPg.Finish()
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
	log.Println("будет записано файлов:", len(toWrite))

	pg := progressbar.NewOptions64(toWriteBytesCount,
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetElapsedTime(true),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionShowDescriptionAtLineEnd(),
		progressbar.OptionThrottle(time.Millisecond*50),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	counter := 0
	errors := make([]string, 0)
	parentCreated := false

	for _, path := range toWrite {
		info, err := os.Stat(path)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}
		_, filename := filepath.Split(path)
		pg.Describe(formatFilename(filename))
		ext := filepath.Ext(path)
		name := fmt.Sprintf("%010d%s", counter, ext)
		counter++
		newPath := filepath.Join(to, name)
		if !parentCreated {
			parentDir := filepath.Dir(newPath)
			if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
				pg.Finish()
				pg.Clear()
				log.Fatalln("не удалось создать родительские папки")
			}
			parentCreated = true
		}
		err = copyFile(path, newPath)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}
		if checkMd5 {
			if getMd5(path) != getMd5(newPath) {
				errors = append(errors, fmt.Sprintf("md5 %s -> %s не совпали", path, newPath))
			}
		}
		pg.Add(int(info.Size()))
	}

	fmt.Println()

	if len(errors) != 0 {
		log.Println("завершено с ошибками:")
		for _, e := range errors {
			log.Println(e)
		}
		os.Exit(1)
	} else {
		log.Println("завершено без ошибок")
	}
}
