package common

import (
	"fmt"
	"strings"
)

// AboutThisBotWithoutCommands contains the message that explains how to interact with the bot.
const AboutThisBotWithoutCommands = "Instructions for interacting with me are available [here](%s). If you have questions or suggestions related to my behavior, please file an issue against the [gitbot](https://github.com/gitpod-io/gitbot/issues/new) repository."

// FormatResponseRaw nicely formats a response for one does not have an issue comment
func FormatResponseRaw(body, bodyURL, login, reply, instructionsURL string) string {
	format := `In response to [this](%s):

%s
`
	// Quote the user's comment by prepending ">" to each line.
	var quoted []string
	for _, l := range strings.Split(body, "\n") {
		quoted = append(quoted, ">"+l)
	}
	return FormatResponse(login, reply, fmt.Sprintf(format, bodyURL, strings.Join(quoted, "\n")), instructionsURL)
}

// FormatResponse nicely formats a response to a generic reason.
func FormatResponse(to, message, reason, instructionsURL string) string {
	format := `@%s: %s

<details>

%s

%s
</details>`

	return fmt.Sprintf(format, to, message, reason, fmt.Sprintf(AboutThisBotWithoutCommands, instructionsURL))
}
