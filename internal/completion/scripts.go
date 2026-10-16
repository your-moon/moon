package completion

import (
	"bytes"
	"fmt"
	"text/template"
)

// Script returns the completion script for shell ("zsh", "bash" or "fish"). prog is the command name the
// script completes. The scripts only forward the words to `prog __complete`; all logic is in Go.
func Script(shell, prog string) (string, error) {
	var src string
	switch shell {
	case "zsh":
		src = zshTmpl
	case "bash":
		src = bashTmpl
	case "fish":
		src = fishTmpl
	default:
		return "", fmt.Errorf("unknown shell %q (want zsh, bash or fish)", shell)
	}
	t := template.Must(template.New(shell).Parse(src))
	var b bytes.Buffer
	if err := t.Execute(&b, map[string]string{"Prog": prog}); err != nil {
		return "", err
	}
	return b.String(), nil
}

const zshTmpl = `#compdef {{.Prog}}
# zsh completion for {{.Prog}}. Install: source <({{.Prog}} completion zsh)
_{{.Prog}}() {
  local -a out vals disp
  local line dir v d
  out=("${(@f)$("${words[1]}" __complete "${(@Q)words[2,CURRENT]}" 2>/dev/null)}")
  (( ${#out} )) || return 1
  dir=${out[-1]#:}
  out[-1]=()
  for line in "${out[@]}"; do
    [[ -n $line ]] || continue
    v=${line%%$'\t'*}
    vals+=("$v")
    if [[ $line == *$'\t'* && -n ${line#*$'\t'} ]]; then
      disp+=("$v  -- ${line#*$'\t'}")
    else
      disp+=("$v")
    fi
  done
  if (( ${#vals} )); then
    compadd -V candidates -l -d disp -a vals
  elif (( dir != 4 )); then
    _files
  fi
}
if [[ ${funcstack[1]} == _{{.Prog}} ]]; then
  _{{.Prog}} "$@"
else
  (( $+functions[compdef] )) || { autoload -Uz compinit && compinit -i; }
  compdef _{{.Prog}} {{.Prog}}
fi
`

const bashTmpl = `# bash completion for {{.Prog}}. Install: source <({{.Prog}} completion bash)
_{{.Prog}}_complete() {
  local cur line dir i n
  local -a out
  cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  while IFS= read -r line; do out+=("$line"); done < <("${COMP_WORDS[0]}" __complete --shell bash --cur "$cur" --line "${COMP_LINE:0:COMP_POINT}" 2>/dev/null)
  n=${#out[@]}
  (( n > 0 )) || return 0
  dir=${out[n-1]#:}
  unset "out[n-1]"
  COMPREPLY=("${out[@]}")
  if (( ${#COMPREPLY[@]} == 0 )) && [[ $dir != 4 ]]; then
    COMPREPLY=($(compgen -f -- "$cur"))
    type compopt >/dev/null 2>&1 && compopt -o filenames 2>/dev/null
  fi
  return 0
}
complete -F _{{.Prog}}_complete {{.Prog}}
`

const fishTmpl = `# fish completion for {{.Prog}}. Install: {{.Prog}} completion fish | source
function __{{.Prog}}_complete
    set -l toks (commandline -opc)
    set -l cur (commandline -ct)
    set -l out ($toks[1] __complete $toks[2..-1] "$cur" 2>/dev/null)
    set -l dir 0
    if test (count $out) -gt 0
        set dir (string replace -r '^:' '' -- $out[-1])
        set -e out[-1]
    end
    if test (count $out) -eq 0; and test "$dir" != 4
        __fish_complete_path "$cur"
        return
    end
    printf '%s\n' $out
end
complete -c {{.Prog}} -f -a '(__{{.Prog}}_complete)'
`
