package ortfodb

import (
	"fmt"
	"io"
	"os"
	"strings"

	// "time"

	// "github.com/mattn/go-isatty"
	"github.com/mitchellh/colorstring"
	// "github.com/theckman/yacspin"
	"github.com/xeipuuv/gojsonschema"
)

func logWriter() io.Writer {
	var writer io.Writer = os.Stderr
	if progressBars != nil {
		writer = progressBars.Bypass()
	}
	return writer
}

func LogCustom(verb string, color string, message string, fmtArgs ...interface{}) {
	fmt.Fprintln(logWriter(), colorstring.Color(fmt.Sprintf("[bold][%s]%15s[reset] %s", color, verb, fmt.Sprintf(message, fmtArgs...))))
}

// DisplayValidationErrors takes in a slice of json schema validation errors and displays them nicely to in the terminal.
func DisplayValidationErrors(errors []gojsonschema.ResultError, filename string) {
	println("Your " + filename + " file is invalid. Here are the validation errors:\n")
	for _, err := range errors {
		/* FIXME: having a "." in the field name fucks up the display: eg:

		   - 0/media/fr-FR/2/online
		   Invalid type. Expected: boolean, given: string

		   if I replace fr-FR with fr.FR in the JSON:

		   			   ↓
		   - 0/media/fr/FR/2/online
		   Invalid type. Expected: boolean, given: string
		*/
		colorstring.Println("- " + strings.ReplaceAll(err.Field(), ".", "[blue][bold]/[reset]"))
		colorstring.Println("    [red]" + err.Description())
	}
}

// LogError logs non-fatal errors.
func (ctx *RunContext) LogError(message string, fmtArgs ...interface{}) {
	// colorstring.Fprintf(logWriter(), "[red]          Error[reset] %s\n", fmt.Sprintf(message, fmtArgs...))
	LogCustom("Error", "red", message, fmtArgs...)
}

// LogInfo logs infos.
func (ctx *RunContext) LogInfo(message string, fmtArgs ...interface{}) {
	LogCustom("Info", "blue", message, fmtArgs...)
}

// LogDebug logs debug information.
func (ctx *RunContext) LogDebug(message string, fmtArgs ...interface{}) {
	if os.Getenv("DEBUG") == "" {
		return
	}
	LogCustom("Debug", "magenta", message, fmtArgs...)
}

// LogWarning logs warnings.
func (ctx *RunContext) LogWarning(message string, fmtArgs ...interface{}) {
	LogCustom("Warning", "yellow", message, fmtArgs...)
}
