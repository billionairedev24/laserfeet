# Laser Feet for VS Code

Language support for [Laser Feet](https://billionairedev24.github.io/laserfeet/) `.lf` files:

- **Live errors as you type**, from `lf check`, underlined in red and listed in the Problems panel
- **Autocomplete**: fields and beams after a dot, names in scope, keywords, built-in beams and types
- **Hover** over a name to see its type
- VS Code recognizes `.lf` files as **Laser Feet** (shown in the status bar, with a file icon)
- Syntax highlighting, including types and `{values}` inside text
- Bracket matching and auto-closing, `--` comment toggling (Ctrl/⌘ + /), auto-indent after `{`
- Snippets with types: `launch`, `beam`, `object`, `field`, `rule`, `when`, `whenotherwise`, `each`, `loop`, `attempt`, `test`, `server`, `get`

Live errors, autocomplete and hover use the `lf` program, so install Laser Feet first. If `lf` isn't on your PATH, set **Laser Feet: Path** in your user settings.

## Install

Download `laserfeet.vsix` from the [latest release](https://github.com/billionairedev24/laserfeet/releases/latest), check it against `SHA256SUMS`, then:

    code --install-extension laserfeet.vsix

Or in VS Code: Extensions view → `…` menu → **Install from VSIX…**

## License

Same license as the Laser Feet repository.
