"""Sleep controls with synthetic API fixtures; never connects to a bot."""
import argparse
import json
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright


def run(base_url):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, channel="chrome")
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        errors, wakes = [], []
        page.on("pageerror", lambda error: errors.append(str(error)))
        sleep = {
            "sleeping": True, "pending_wake": False, "started_at": "2026-11-16T21:00:00+08:00",
            "wake_at": "2026-11-16T21:10:00+08:00", "reason": "暂时歇一会",
            "threshold": 5, "window_seconds": 30, "directed_count": 2,
            "consolidation": "running", "consolidated": 1, "memory_count": 3,
        }

        def api(route):
            request = route.request
            path = urlparse(request.url).path
            if path == "/api/runtime":
                body = {"available": True, "phase": "sleeping" if sleep["sleeping"] else "idle",
                        "sleep": sleep, "metrics": {}}
            elif path == "/api/sleep/wake":
                assert request.method == "POST"
                wakes.append(path)
                sleep.update(sleeping=False, wake_reason="webui", consolidation="interrupted")
                body = sleep
            elif path == "/api/notes":
                body = []
            elif path == "/api/mood":
                body = {"mood": "平静"}
            elif path == "/api/timers":
                body = []
            elif path == "/api/config":
                body = {"effective": {"agent": {"sleep": {"enabled": True, "min_seconds": 180,
                        "max_seconds": 900, "window_seconds": 30, "threshold": 5}}}}
            else:
                body = {}
            route.fulfill(status=200, content_type="application/json",
                          body=json.dumps(body, ensure_ascii=False))

        page.route("**/api/**", api)
        page.goto(base_url.rstrip("/") + "/#agent")
        page.wait_for_load_state("networkidle")
        page.get_by_label("访问令牌", exact=True).fill("preview-token")
        page.get_by_role("button", name="进入控制台", exact=True).click()
        panel = page.locator('[aria-label="作息与记忆整理"]')
        expect(panel.get_by_text("休息中", exact=True)).to_be_visible()
        expect(panel.get_by_text("2 / 5", exact=True)).to_be_visible()
        for width, height in ((1440, 900), (390, 844)):
            page.set_viewport_size({"width": width, "height": height})
            assert page.locator("html").evaluate("(el) => el.scrollWidth <= innerWidth")
            page.screenshot(path=f".tools/sleep-{width}.png", full_page=True)
        panel.get_by_role("button", name="立即唤醒", exact=True).click()
        expect(panel.get_by_text("清醒", exact=True)).to_be_visible()
        expect(panel.get_by_role("button", name="立即唤醒", exact=True)).to_be_disabled()
        assert len(wakes) == 1
        panel.get_by_role("button", name="作息设置").click()
        expect(page.get_by_label("最短休息（秒）")).to_have_value("180")
        expect(page.get_by_label("提前唤醒消息数")).to_have_value("5")
        assert not errors, errors
        browser.close()
    print("Sleep browser checks passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:6094")
    run(parser.parse_args().base_url)
