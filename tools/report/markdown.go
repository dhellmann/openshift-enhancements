package report

import (
	"github.com/russross/blackfriday"
	"github.com/shurcooL/markdownfmt/markdown"
)

func Cleanup(input []byte) []byte {
	// extensions for GitHub Flavored Markdown-like parsing.
	const extensions = blackfriday.EXTENSION_NO_INTRA_EMPHASIS |
		blackfriday.EXTENSION_TABLES |
		blackfriday.EXTENSION_FENCED_CODE |
		blackfriday.EXTENSION_AUTOLINK |
		blackfriday.EXTENSION_STRIKETHROUGH |
		blackfriday.EXTENSION_SPACE_HEADERS |
		blackfriday.EXTENSION_NO_EMPTY_LINE_BEFORE_BLOCK

	opt := &markdown.Options{
		Terminal: false, // do not include ANSI escape codes in output
	}

	output := blackfriday.Markdown(input, markdown.NewRenderer(opt), extensions)
	return output
}
