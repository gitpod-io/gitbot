# willkommen

> Welcome the new external contributors and automatically apply labels to their pull requests

It says hello in different languages, choosing randomly from:

- German
- Italian
- English
- Spanish

The message template can interpolate the following variables:

- `.Greeting`
- `.AuthorLogin`
- `.AuthorName`
- `.Org`
- `.Repo`
- `.Type`

You can also instruct this plugin to apply a label to the newly opened pull requests.

This behavior can be disable by leaving the `label` configuration key empty (or by not defining it).

## Configuration

Here's an example configuration applying the behavior to all the repositories in a GitHub organization.

```yaml
orgsRepos:
  "gitpod-io":
    label: "community-contribution"
    message: "{{.Greeting}} @{{.AuthorLogin}}! It looks like this is your first {{.Type}} to {{.Org}}/{{.Repo}} 🎉"
```

