Goober Bot - Telegram Bot

A simple Telegram bot built with Go that responds to greeting messages.

## Setup

1. Get a bot token from [@BotFather](https://t.me/BotFather) on Telegram
2. Set your token as an environment variable:
   ```
   $env:TELEGRAM_BOT_TOKEN="your_bot_token_here"
   ```
   or
   ```bash
   export TELEGRAM_BOT_TOKEN="your_bot_token_here"
   ```
3. Run the bot:
   ```bash
   go run main.go
   ```

## Features

- Responds to "hi", "Hi", "/hi", and various greetings in different languages
- Simply displays "Hi, I'm Goober Bot!"

## Usage

1. Add the bot to your Telegram chat
2. Send "hi" or any of the supported greetings
3. The bot will automatically respond