package lang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/hashicorp/go-hclog"
	"github.com/j178/leetgo/config"
	"github.com/j178/leetgo/leetcode"
	"github.com/j178/leetgo/utils"
	"github.com/spf13/viper"
)

var (
	NotSupported   = errors.New("not supported")
	NotImplemented = errors.New("not implemented")
)

type Generator interface {
	Name() string
	ShortName() string
	Slug() string
	// Generate generates code files for the question.
	Generate(q *leetcode.QuestionData) ([]FileOutput, error)
}

type Testable interface {
	// CheckLibrary checks if the library is installed. Return false if not installed.
	CheckLibrary() bool
	// GenerateLibrary copies necessary supporting library files to project.
	GenerateLibrary() error
	RunTest(q *leetcode.QuestionData) error
}

type baseLang struct {
	name              string
	slug              string
	shortName         string
	extension         string
	lineComment       string
	blockCommentStart string
	blockCommentEnd   string
}

func (l baseLang) Name() string {
	return l.name
}

func (l baseLang) Slug() string {
	return l.slug
}

func (l baseLang) ShortName() string {
	return l.shortName
}

// TODO use template
func (l baseLang) generateComments(q *leetcode.QuestionData) string {
	var content []string
	cfg := config.Get()
	now := time.Now().Format("2006/01/02 15:04")
	content = append(content, fmt.Sprintf("%s Created by %s at %s", l.lineComment, cfg.Author, now))
	content = append(content, fmt.Sprintf("%s %s", l.lineComment, q.Url()))
	if q.IsContest() {
		content = append(content, fmt.Sprintf("%s %s", l.lineComment, q.ContestUrl()))
	}
	content = append(content, "")
	content = append(content, l.blockCommentStart)
	content = append(content, fmt.Sprintf("%s.%s (%s)", q.QuestionFrontendId, q.GetTitle(), q.Difficulty))
	content = append(content, "")
	content = append(content, q.GetFormattedContent())
	content = append(content, l.blockCommentEnd)
	content = append(content, "")
	return strings.Join(content, "\n")
}

type Modifier func(string, *leetcode.QuestionData) string

func (l baseLang) generateCode(q *leetcode.QuestionData, modifiers ...Modifier) string {
	code := q.GetCodeSnippet(l.Slug())
	for _, m := range modifiers {
		code = m(code, q)
	}
	return code
}

func addCodeMark(commentMark string) Modifier {
	return func(s string, q *leetcode.QuestionData) string {
		cfg := config.Get()
		return fmt.Sprintf(
			"%s %s\n\n%s\n\n%s %s",
			commentMark,
			cfg.Code.CodeBeginMark,
			s,
			commentMark,
			cfg.Code.CodeEndMark,
		)
	}
}

func removeComments(code string, q *leetcode.QuestionData) string {
	return code
}

func prepend(s string) Modifier {
	return func(code string, q *leetcode.QuestionData) string {
		return s + code
	}
}

func getFilenameTemplate(gen Generator) string {
	res := config.Get().Code.FilenameTemplate
	if res != "" {
		return res
	}
	res = viper.GetString("code." + gen.Slug() + ".filename_template")
	return res
}

func (l baseLang) Generate(q *leetcode.QuestionData) ([]FileOutput, error) {
	comment := l.generateComments(q)
	code := l.generateCode(q, addCodeMark(l.lineComment))
	content := comment + "\n" + code + "\n"

	filenameTmpl := getFilenameTemplate(l)
	baseFilename, err := q.GetFormattedFilename(l.slug, filenameTmpl)
	if err != nil {
		return nil, err
	}

	files := FileOutput{
		Path:    baseFilename + "." + l.extension,
		Content: content,
	}
	return []FileOutput{files}, nil
}

type FileOutput struct {
	Path      string
	Content   string
	Created   bool
	Generator Generator
}

func GetGenerator(gen string) Generator {
	gen = strings.ToLower(gen)
	for _, l := range SupportedLangs {
		if strings.HasPrefix(l.ShortName(), gen) || strings.HasPrefix(l.Slug(), gen) {
			return l
		}
	}
	return nil
}

func Generate(q *leetcode.QuestionData) ([]FileOutput, error) {
	cfg := config.Get()
	gen := GetGenerator(cfg.Code.Lang)
	if gen == nil {
		return nil, fmt.Errorf("language %s is not supported yet, welcome to send a PR", cfg.Code.Lang)
	}

	codeSnippet := q.GetCodeSnippet(gen.Slug())
	if codeSnippet == "" {
		return nil, fmt.Errorf("no %s code snippet found for %s", cfg.Code.Lang, q.TitleSlug)
	}

	if gen, ok := gen.(Testable); ok && !gen.CheckLibrary() {
		err := gen.GenerateLibrary()
		if err != nil {
			return nil, err
		}
	}

	files, err := gen.Generate(q)
	if err != nil {
		return nil, err
	}

	dir := viper.GetString("code." + cfg.Code.Lang + ".out_dir")
	if dir == "" {
		dir = cfg.Code.Lang
	}

	for i := range files {
		path := filepath.Join(cfg.ProjectRoot(), dir, files[i].Path)
		files[i].Path = path
		files[i].Generator = gen
		written, err := tryWrite(path, files[i].Content)
		if err != nil {
			hclog.L().Error("failed to write file", "path", path, "err", err)
			continue
		}
		files[i].Created = written
	}

	state := config.LoadState()
	state.LastGenerated = config.LastGeneratedQuestion{
		Slug:       q.TitleSlug,
		FrontendID: q.QuestionFrontendId,
		Gen:        gen.Slug(),
	}
	config.SaveState(state)

	return files, nil
}

func tryWrite(file string, content string) (bool, error) {
	write := true
	if utils.IsExist(file) {
		if !viper.GetBool("yes") {
			prompt := &survey.Confirm{Message: fmt.Sprintf("File \"%s\" already exists, overwrite?", file)}
			err := survey.AskOne(prompt, &write)
			if err != nil {
				return false, err
			}
		}
	}
	if !write {
		return false, nil
	}

	err := utils.CreateIfNotExists(file, false)
	if err != nil {
		return false, err
	}
	err = os.WriteFile(file, []byte(content), 0644)
	if err != nil {
		return false, err
	}
	hclog.L().Info("generated", "file", file)
	return true, nil
}
