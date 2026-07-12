#!/bin/bash

COLOR_RED="\033[0;31m"
COLOR_CYAN="\033[0;36m"
COLOR_RESET="\033[0m"

RUN_CMD="go run cmd/clabernetes/main.go run"

go mod tidy

if [[ "${1:-}" == "--run" ]]; then
    exec go run cmd/clabernetes/main.go run
fi

echo -e "${COLOR_RED}
 _______  ___      _______  _______  _______  ______    __    _  _______  _______  _______  _______
|       ||   |    |   _   ||  _    ||       ||    _ |  |  |  | ||       ||       ||       ||       |
|       ||   |    |  |_|  || |_|   ||    ___||   | ||  |   |_| ||    ___||_     _||    ___||  _____|
|       ||   |    |       ||       ||   |___ |   |_||_ |       ||   |___   |   |  |   |___ | |_____
|      _||   |___ |       ||  _   | |    ___||    __  ||  _    ||    ___|  |   |  |    ___||_____  |
|     |_ |       ||   _   || |_|   ||   |___ |   |  | || | |   ||   |___   |   |  |   |___  _____| |
|_______||_______||__| |__||_______||_______||___|  |_||_|  |__||_______|  |___|  |_______||_______|

${COLOR_RESET}

To run: ${COLOR_CYAN}${RUN_CMD}${COLOR_RESET}

"

export HISTFILE=/tmp/.bash_history
history -s "$RUN_CMD"
history -a

bash