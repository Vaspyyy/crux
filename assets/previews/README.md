# Re-recording the README previews

The previews are recorded with [VHS](https://github.com/charmbracelet/vhs) in an anonymous demo workspace.

```sh
python3 make_demo.py                       # builds /var/tmp/crux-demo (a Rust crate and a Python package)
export RUSTUP_HOME=~/.rustup CARGO_HOME=~/.cargo PYTHONUSERBASE=~/.local
cd /var/tmp/crux-demo && mkdir -p tapes && cp <this folder>/*.tape tapes/ && cd tapes
for t in hero agent; do vhs $t.tape & done; wait

cp hero.gif <repo>/assets/
ffmpeg -y -sseof -0.05 -i agent.mp4 -frames:v 1 <repo>/assets/agent.png

rm -rf /var/tmp/crux-demo
```

Needs `vhs` (with `ttyd` and `ffmpeg`), cargo, pytest and a JetBrainsMono Nerd Font.
`common.tape` sets a fake `HOME`, so no real user or path ends up in the images; the toolchain
variables only point the tools at their installations and are typed while the recording is hidden.
