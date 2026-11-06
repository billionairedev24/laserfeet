# Laser Feet for VS Code

Language support for [Laser Feet](https://billionairedev24.github.io/laserfeet/) `.lf` files:

- VS Code recognizes `.lf` files as **Laser Feet** (shown in the status bar, with a file icon)
- Syntax highlighting, including types and `{values}` inside text
- Bracket matching and auto-closing, `--` comment toggling (Ctrl/⌘ + /), auto-indent after `{`
- Snippets with types: `launch`, `beam`, `object`, `field`, `rule`, `when`, `whenotherwise`, `each`, `loop`, `attempt`, `test`, `server`, `get`

## Install

Download `laserfeet.vsix` from the [latest release](https://github.com/billionairedev24/laserfeet/releases/latest), check it against `SHA256SUMS`, then:

    code --install-extension laserfeet.vsix

Or in VS Code: Extensions view → `…` menu → **Install from VSIX…**

## License

Same license as the Laser Feet repository.
