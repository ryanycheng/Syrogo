#!/usr/bin/env python3
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path


class E2E:
    def __init__(self, base_url: str, config_path: str, timeout: int, run_claude: bool, run_codex: bool):
        self.base_url = base_url.rstrip("/")
        self.config_path = Path(config_path)
        self.timeout = timeout
        self.run_claude = run_claude
        self.run_codex = run_codex
        self.failed = 0
        self.skipped = 0
        self.tokens = {}
        self.admin_token = os.environ.get("SYROGO_ACCOUNTING_ADMIN_TOKEN", "")
        self.tmpdirs = []

    def log(self, message: str) -> None:
        print(f"[e2e] {message}")

    def pass_(self, name: str, detail: str = "") -> None:
        suffix = f" {detail}" if detail else ""
        self.log(f"PASS: {name}{suffix}")

    def warn(self, name: str, detail: str = "") -> None:
        suffix = f" {detail}" if detail else ""
        self.log(f"WARN: {name}{suffix}")

    def fail(self, name: str, detail: str = "") -> None:
        suffix = f" {detail}" if detail else ""
        print(f"[e2e] FAIL: {name}{suffix}", file=sys.stderr)
        self.failed += 1

    def skip(self, name: str, detail: str = "") -> None:
        suffix = f" {detail}" if detail else ""
        self.log(f"SKIP: {name}{suffix}")
        self.skipped += 1

    def load_config_tokens(self) -> None:
        if not self.config_path.exists():
            self.warn("config token load", f"missing config: {self.config_path}")
            return
        text = self.config_path.read_text(errors="replace")
        current = None
        for line in text.splitlines():
            name_match = re.match(r'\s*- name:\s*"([^"]+-key)"', line)
            if name_match:
                current = name_match.group(1)
                continue
            token_match = re.match(r'\s*token:\s*"([^"]+)"', line)
            if token_match and current:
                self.tokens[current] = token_match.group(1)
                current = None
        admin_match = re.search(r'admin_token:\s*"([^"]+)"', text)
        if admin_match and not self.admin_token:
            self.admin_token = admin_match.group(1)

    def env_or_config_token(self, env_name: str, config_name: str) -> str:
        return os.environ.get(env_name) or self.tokens.get(config_name, "")

    def token(self, config_name: str) -> str:
        return self.tokens.get(config_name, "")

    def request(self, name: str, method: str, path: str, token: str = "", body=None, stream: bool = False):
        url = f"{self.base_url}{path}"
        data = None if body is None else json.dumps(body).encode()
        headers = {}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(url, data=data, method=method, headers=headers)
        started = time.time()
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read().decode(errors="replace")
                elapsed = time.time() - started
                if not (200 <= resp.status < 300):
                    self.fail(name, f"HTTP {resp.status}")
                    return None, raw
                if stream and not ("data:" in raw or "event:" in raw):
                    self.fail(name, "missing SSE frames")
                    return None, raw
                return {"status": resp.status, "headers": dict(resp.headers), "elapsed": elapsed, "body": raw}, raw
        except urllib.error.HTTPError as err:
            raw = err.read().decode(errors="replace")
            self.fail(name, f"HTTP {err.code}: {one_line(raw)}")
            return None, raw
        except Exception as err:
            self.fail(name, f"{type(err).__name__}: {err}")
            return None, ""

    def check_health(self) -> None:
        resp, _ = self.request("healthz", "GET", "/healthz")
        if resp:
            self.pass_("healthz", f"HTTP {resp['status']}")

    def check_matrix(self) -> None:
        checks = [
            ("openai_chat_to_chat", "/v1/chat/completions", "openai-to-chat-key", chat_body("Say pong only.")),
            ("openai_chat_to_responses", "/v1/chat/completions", "openai-to-responses-key", chat_body("Say pong only.")),
            ("openai_chat_to_anthropic", "/v1/chat/completions", "openai-to-anthropic-key", chat_body("Say pong only.")),
            ("responses_to_chat", "/v1/responses", "responses-to-chat-key", responses_body("Say pong only.")),
            ("responses_to_responses", "/v1/responses", "responses-to-responses-key", responses_body("Say pong only.")),
            ("responses_to_anthropic", "/v1/responses", "responses-to-anthropic-key", responses_body("Say pong only.")),
            ("anthropic_to_chat", "/v1/messages", "anthropic-to-chat-key", messages_body("Say pong only.")),
            ("anthropic_to_responses", "/v1/messages", "anthropic-to-responses-key", messages_body("Say pong only.")),
            ("anthropic_to_anthropic", "/v1/messages", "anthropic-to-anthropic-key", messages_body("Say pong only.")),
        ]
        for name, path, key, body in checks:
            token = self.token(key)
            if not token:
                self.skip(name, f"missing config token {key}")
                continue
            resp, raw = self.request(name, "POST", path, token, body)
            if resp:
                self.pass_(name, f"HTTP {resp['status']} {resp['elapsed']:.1f}s {sample(raw)}")

    def check_streaming(self) -> None:
        checks = [
            ("openai_chat_stream", "/v1/chat/completions", "openai-to-chat-key", chat_body("Say pong only.", stream=True), "data: [DONE]"),
            ("anthropic_messages_stream", "/v1/messages", "anthropic-to-anthropic-key", messages_body("Say pong only.", stream=True), "event: done"),
        ]
        for name, path, key, body, marker in checks:
            token = self.token(key)
            if not token:
                self.skip(name, f"missing config token {key}")
                continue
            resp, raw = self.request(name, "POST", path, token, body, stream=True)
            if resp and marker not in raw:
                self.fail(name, f"missing stream terminator {marker!r}")
                continue
            if resp:
                self.pass_(name, f"HTTP {resp['status']} {resp['elapsed']:.1f}s")

    def check_multi_turn(self) -> None:
        word = "syrogo-e2e"
        chat_token = self.token("openai-to-chat-key")
        if chat_token:
            body = {
                "model": "ignored-by-route",
                "messages": [
                    {"role": "user", "content": f"Remember this word: {word}."},
                    {"role": "assistant", "content": f"I will remember the word {word}."},
                    {"role": "user", "content": "What word did I ask you to remember? Reply with only the word."},
                ],
            }
            resp, raw = self.request("multi_turn_openai_chat", "POST", "/v1/chat/completions", chat_token, body)
            if resp and word in raw.lower():
                self.pass_("multi_turn_openai_chat", f"HTTP {resp['status']}")
            elif resp:
                self.fail("multi_turn_openai_chat", f"missing {word}")
        else:
            self.skip("multi_turn_openai_chat", "missing config token openai-to-chat-key")

        messages_token = self.token("anthropic-to-chat-key")
        if messages_token:
            body = {
                "model": "ignored-by-route",
                "max_tokens": 128,
                "messages": [
                    {"role": "user", "content": [{"type": "text", "text": f"Remember this word: {word}."}]},
                    {"role": "assistant", "content": [{"type": "text", "text": f"I will remember the word {word}."}]},
                    {"role": "user", "content": [{"type": "text", "text": "What word did I ask you to remember? Reply with only the word."}]},
                ],
            }
            resp, raw = self.request("multi_turn_anthropic_messages", "POST", "/v1/messages", messages_token, body)
            if resp and word in raw.lower():
                self.pass_("multi_turn_anthropic_messages", f"HTTP {resp['status']}")
            elif resp:
                self.fail("multi_turn_anthropic_messages", f"missing {word}")
        else:
            self.skip("multi_turn_anthropic_messages", "missing config token anthropic-to-chat-key")

    def check_tool_roundtrip(self) -> None:
        token = self.token("openai-to-chat-key")
        if not token:
            self.skip("tool_roundtrip_openai_chat", "missing config token openai-to-chat-key")
            return
        tool = {
            "type": "function",
            "function": {
                "name": "get_city_weather",
                "description": "Return weather for a city.",
                "parameters": {
                    "type": "object",
                    "properties": {"city": {"type": "string"}},
                    "required": ["city"],
                },
            },
        }
        first_body = {
            "model": "ignored-by-route",
            "messages": [{"role": "user", "content": "Use get_city_weather for Shanghai, then answer with the returned weather."}],
            "tools": [tool],
            "tool_choice": {"type": "function", "function": {"name": "get_city_weather"}},
        }
        resp, raw = self.request("tool_roundtrip_openai_chat_step1", "POST", "/v1/chat/completions", token, first_body)
        if not resp:
            return
        try:
            data = json.loads(raw)
            message = data["choices"][0]["message"]
            tool_calls = message.get("tool_calls") or []
            call = tool_calls[0]
            call_id = call["id"]
            name = call["function"]["name"]
            arguments = call["function"].get("arguments", "")
        except Exception as err:
            self.fail("tool_roundtrip_openai_chat_step1", f"invalid tool call response: {err}")
            return
        if name != "get_city_weather" or not call_id or "Shanghai" not in arguments:
            self.fail("tool_roundtrip_openai_chat_step1", "tool call name/id/arguments mismatch")
            return
        second_body = {
            "model": "ignored-by-route",
            "messages": [
                {"role": "user", "content": "Use get_city_weather for Shanghai, then answer with the returned weather."},
                {"role": "assistant", "content": message.get("content") or "", "tool_calls": tool_calls},
                {"role": "tool", "tool_call_id": call_id, "content": '{"weather":"sunny","temperature_c":26}'},
            ],
            "tools": [tool],
        }
        resp, raw = self.request("tool_roundtrip_openai_chat_step2", "POST", "/v1/chat/completions", token, second_body)
        if resp and "sunny" in raw.lower() and "26" in raw:
            self.pass_("tool_roundtrip_openai_chat", "tool_call + tool_result + final answer")
        elif resp:
            self.fail("tool_roundtrip_openai_chat", "final response did not use tool result")

    def check_usage(self) -> None:
        if not self.admin_token:
            self.skip("usage_stats", "missing SYROGO_ACCOUNTING_ADMIN_TOKEN and config admin_token")
            return
        for name, path in [
            ("usage_by_key", "/stats/usage?group_by=key"),
            ("usage_by_provider", "/stats/usage?group_by=provider"),
        ]:
            resp, raw = self.request(name, "GET", path, self.admin_token)
            if resp:
                self.pass_(name, f"HTTP {resp['status']} {sample(raw)}")

    def check_claude(self) -> None:
        if not self.run_claude:
            self.skip("claude_code", "set SYROGO_E2E_CLAUDE=1 to enable")
            return
        if not shutil.which("claude"):
            self.skip("claude_code", "claude CLI not found")
            return
        token = self.token("anthropic-to-chat-key") or os.environ.get("SYROGO_E2E_CLAUDE_TOKEN", "")
        if not token:
            self.skip("claude_code", "missing anthropic-to-chat-key token")
            return
        env = os.environ.copy()
        env["ANTHROPIC_BASE_URL"] = self.base_url
        env["ANTHROPIC_AUTH_TOKEN"] = token
        tests = [
            ("claude_code_single", ["claude", "--bare", "-p", "--model", "claude-sonnet-4-6", "--output-format", "text", "Reply with exactly: syrogo-claude-ok"], "syrogo-claude-ok"),
            ("claude_code_tool", ["claude", "--bare", "-p", "--model", "claude-sonnet-4-6", "--output-format", "text", "Read the Makefile and list the available make targets. Do not modify files."], "smoke"),
        ]
        for name, cmd, marker in tests:
            result = subprocess.run(cmd, env=env, text=True, input="", capture_output=True, timeout=self.timeout)
            combined = result.stdout + result.stderr
            if result.returncode != 0:
                self.fail(name, f"exit={result.returncode}: {sample(combined)}")
            elif marker not in combined:
                self.fail(name, f"missing marker {marker!r}: {sample(combined)}")
            else:
                self.pass_(name)

    def check_codex(self) -> None:
        if not self.run_codex:
            self.skip("codex", "set SYROGO_E2E_CODEX=1 to enable")
            return
        if not shutil.which("codex"):
            self.skip("codex", "codex CLI not found")
            return
        token = self.token("responses-to-chat-key") or os.environ.get("SYROGO_E2E_CODEX_TOKEN", "")
        if not token:
            self.skip("codex", "missing responses-to-chat-key token")
            return
        source_config = Path.home() / ".codex" / "config.toml"
        if not source_config.exists():
            self.skip("codex", "~/.codex/config.toml not found")
            return
        codex_home = Path(tempfile.mkdtemp(prefix="syrogo-codex-"))
        self.tmpdirs.append(codex_home)
        config = source_config.read_text(errors="replace")
        config = re.sub(r'base_url\s*=\s*"[^"]+"', f'base_url = "{self.base_url}/v1"', config, count=1)
        config = re.sub(r'model\s*=\s*"[^"]+"', 'model = "gpt-5.4"', config, count=1)
        (codex_home / "config.toml").write_text(config)
        (codex_home / "auth.json").write_text(json.dumps({"OPENAI_API_KEY": token}))
        env = os.environ.copy()
        env["CODEX_HOME"] = str(codex_home)
        tests = [
            ("codex_single", "Reply with exactly: syrogo-codex-ok", "syrogo-codex-ok"),
            ("codex_tool", "Inspect the Makefile and list the available targets. Do not modify files.", "smoke"),
        ]
        for name, prompt, marker in tests:
            out_file = codex_home / f"{name}.out"
            cmd = ["codex", "exec", "--skip-git-repo-check", "-s", "read-only", "--output-last-message", str(out_file), prompt]
            result = subprocess.run(cmd, env=env, text=True, input="", capture_output=True, timeout=self.timeout)
            combined = result.stdout + result.stderr
            if result.returncode != 0:
                self.fail(name, f"exit={result.returncode}: {sample(combined)}")
            elif marker not in combined and (not out_file.exists() or marker not in out_file.read_text(errors="replace")):
                self.fail(name, f"missing marker {marker!r}: {sample(combined)}")
            else:
                if "failed to parse function arguments" in combined:
                    self.warn(name, "Codex reported malformed function arguments but completed")
                self.pass_(name)

    def cleanup(self) -> None:
        for path in self.tmpdirs:
            shutil.rmtree(path, ignore_errors=True)

    def run(self) -> int:
        self.load_config_tokens()
        self.log(f"base url: {self.base_url}")
        self.check_health()
        self.check_matrix()
        self.check_streaming()
        self.check_multi_turn()
        self.check_tool_roundtrip()
        self.check_usage()
        self.check_claude()
        self.check_codex()
        self.cleanup()
        if self.failed:
            self.log(f"done with failures: failed={self.failed}, skipped={self.skipped}")
            return 1
        self.log(f"done: failed=0, skipped={self.skipped}")
        return 0


def chat_body(prompt: str, stream: bool = False):
    body = {"model": "ignored-by-route", "messages": [{"role": "user", "content": prompt}]}
    if stream:
        body["stream"] = True
    return body


def responses_body(prompt: str):
    return {"model": "ignored-by-route", "input": prompt}


def messages_body(prompt: str, stream: bool = False):
    body = {
        "model": "ignored-by-route",
        "max_tokens": 128,
        "messages": [{"role": "user", "content": [{"type": "text", "text": prompt}]}],
    }
    if stream:
        body["stream"] = True
    return body


def one_line(text: str) -> str:
    return " ".join(text.split())[:260]


def sample(text: str) -> str:
    return one_line(text)[:160]


def main() -> int:
    parser = argparse.ArgumentParser(description="Run Syrogo real E2E checks against a running gateway.")
    parser.add_argument("--base-url", default=os.environ.get("SYROGO_E2E_BASE_URL", os.environ.get("SYROGO_SMOKE_BASE_URL", "http://127.0.0.1:23234")))
    parser.add_argument("--config", default=os.environ.get("SYROGO_E2E_CONFIG", "configs/config.yaml"))
    parser.add_argument("--timeout", type=int, default=int(os.environ.get("SYROGO_E2E_TIMEOUT", "120")))
    parser.add_argument("--claude", action="store_true", default=os.environ.get("SYROGO_E2E_CLAUDE") == "1")
    parser.add_argument("--codex", action="store_true", default=os.environ.get("SYROGO_E2E_CODEX") == "1")
    args = parser.parse_args()
    return E2E(args.base_url, args.config, args.timeout, args.claude, args.codex).run()


if __name__ == "__main__":
    raise SystemExit(main())
