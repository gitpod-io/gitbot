# Custom Labels

> The default `labels` plugin, but with superpowers

Set up this plugin on repositories where you would like to have slash commands for adding (or removing) labels.

It works on the following GitHub event payloads:

- Body of new issue
- Edit of issue body
- Body of new PR
- Edit of PR body 
- Issue comment
- Edit of issue comment
- PR comment
- Edit of PR comment

## Setup

As any other external Prow plugin:

```yaml
external_plugins:
    "org/repo":
    - name: customlabels
      endpoint: ...
      events:
      - issues
      - issue_comment
      - pull_request
```

## Config

Define the custom labels for every repository in the [customlabels config](../../config/customlabels.yaml).

For example:

```yaml
orgsRepos:
   "org/repo":
     - key: command
       values:
         - hello
         - bye
     - key: aspect
        values:
          - one
          - two
          - "with spaces in the between"
```


## Usage

Given the example above on an issue or on a pull request of the `org/repo` repository the following slash commands will be available:

- `/command hello` applies label `command: hello`
- `/remove-command hello` removes label `command: hello`
- `/aspect "with spaces in the between"` applies label `aspect: with spaces in the between`
- `/aspect with spaces in the between` applies label `aspect: with spaces in the between`
- `/remove-aspect two` removes label `aspect: two`
- `/command bye` applies label `command: bye`
- `/command "bye"` applies label `command: bye`
- `/label command: bye` applies label `command: bye`
- `/label "command: bye"` applies label `command: bye`
- `/remove-label "command: bye"` removes label `command: bye`
- I think you got it now... :)

Notice the bot will comment back when:

- user is trying to remove a label that's not actually set
- user uses an unknown slash command (eg., `/some hello` in the current example)
- user usese an unknown slash command value (eg., `/aspect four` in the current example)

Finally, notice you can take a look at the help of this plugin for a specific repository at the URL `https://prow.gitpod-dev.com/plugins?repo=<org>%2F<repo>` (please substitute organization and repository names).
