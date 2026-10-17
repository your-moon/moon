package issues

// readBodyGuarded is readBody for the commands that replace or set a body (edit, new). A body that was asked
// for but is blank is refused: an empty --body-file almost always means the script that wrote it failed, and
// on `edit` the queued write would erase the issue's body on GitHub. --allow-empty-body says it is meant.
// fileOnly limits the check to --body-file: `new --body ""` is a deliberate, harmless empty issue.
func (a *App) readBodyGuarded(o *Opts, cmd string, fileOnly bool) (string, bool, error) {
	body, has, err := a.readBody(o)
	if err != nil || !has || o.B["allow-empty-body"] || pyStrip(body) != "" {
		return body, has, err
	}
	src := "--body"
	if f := o.str("body-file"); f != "" {
		src = "--body-file " + f
	} else if fileOnly {
		return body, has, nil
	}
	return "", false, die("%s: the body from %s is empty, so nothing was queued (an empty input usually means a script failed); pass --allow-empty-body if you mean it", cmd, src)
}
