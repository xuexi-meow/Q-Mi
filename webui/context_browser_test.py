"""Context controls against synthetic HTTP fixtures; never connects to a bot."""
import argparse
import json
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright


def run(base_url):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, channel="chrome")
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        state = {"version": 1, "text": "original text", "conflict": False}
        actions = []
        metrics = {"estimated_tokens": 70000, "budget": 64000,
                   "fixed_tokens": 2000, "protected_tokens": 3000, "message_count": 1}

        def api(route):
            request = route.request
            path = urlparse(request.url)
            query = parse_qs(path.query)
            entry = {"index": 0, "kind": "output", "role": "assistant",
                     "text": state["text"], "reasoning": "private reasoning",
                     "estimated_tokens": 500, "images": 0}
            status = 200
            if path.path == "/api/runtime":
                body = {"available": True, "phase": "blocked", "metrics": metrics,
                        "last_error": "上下文超出预算", "working_version": state["version"]}
            elif path.path == "/api/chats":
                body = []
            elif path.path == "/api/context":
                body = {"version": state["version"], "source": query.get("source", ["working"])[0],
                        "metrics": metrics, "entries": [entry], "total": 1}
            elif path.path == "/api/context/message":
                body = entry
            elif path.path == "/api/context/edit":
                body = {"version": state["version"], "index": 0, "text": state["text"],
                        "editable": True, "deletable": True, "clear_reasoning": True,
                        "delete_indices": [0]}
            elif path.path == "/api/context/action":
                action = request.post_data_json
                actions.append(action)
                if state["conflict"] and not action["preview"]:
                    status = 409
                    body = {"error": "上下文版本已更新，请刷新列表", "code": "version_conflict"}
                else:
                    if not action["preview"]:
                        state["text"] = action.get("text", state["text"])
                        state["version"] += 1
                    body = {"version": state["version"], "action": action["action"],
                            "affected": [0], "before": 70000, "after": 3000,
                            "budget": 64000, "blocked": False, "preview": action["preview"]}
            else:
                body = {}
            route.fulfill(status=status, content_type="application/json",
                          body=json.dumps(body, ensure_ascii=False))

        page.route("**/api/**", api)
        page.goto(base_url.rstrip("/") + "/#context")
        page.wait_for_load_state("networkidle")
        page.get_by_label("访问令牌", exact=True).fill("preview-token")
        page.get_by_role("button", name="进入控制台", exact=True).click()
        expect(page.get_by_text("上下文已超限，活动暂停", exact=True)).to_be_visible()
        page.locator(".context-row").first.click()
        detail = page.get_by_role("dialog", name="上下文详情")
        detail.get_by_role("button", name="编辑", exact=True).click()
        editor = page.get_by_label("上下文正文", exact=True)
        expect(editor).to_have_value("original text")
        editor.fill("edited text")
        page.once("dialog", lambda dialog: dialog.dismiss())
        page.keyboard.press("Escape")
        expect(editor).to_have_value("edited text")
        detail.get_by_label("清除此条记录的推理内容").check()
        detail.get_by_role("button", name="检查并保存").click()
        review = page.get_by_role("dialog", name="确认上下文变更")
        expect(review).to_be_visible()
        assert actions[-1]["preview"] is True
        assert actions[-1]["text"] == "edited text"
        state["conflict"] = True
        review.get_by_role("button", name="确认保存修改").click()
        expect(review.get_by_role("alert")).to_contain_text("版本已更新")
        review.get_by_role("button", name="返回", exact=True).click()
        expect(editor).to_have_value("edited text")
        state["conflict"] = False
        detail.get_by_role("button", name="检查并保存").click()
        review.get_by_role("button", name="确认保存修改").click()
        expect(detail).not_to_be_visible()
        expect(page.locator(".context-row").first).to_contain_text("edited text")

        for width, height in ((1440, 900), (390, 844)):
            page.set_viewport_size({"width": width, "height": height})
            page.get_by_role("button", name="重置", exact=True).click()
            expect(review).to_contain_text("提示词、当前状态、持久笔记与聊天存档保留")
            assert page.locator("html").evaluate("(el) => el.scrollWidth <= innerWidth")
            page.screenshot(path=f".tools/context-review-{width}.png", full_page=True)
            review.get_by_role("button", name="返回", exact=True).click()
        page.get_by_label("快照来源").select_option("request")
        expect(page.get_by_text("请求快照 · 只读", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="恢复活动", exact=True)).not_to_be_visible()
        assert not errors, errors
        browser.close()
    print("Context browser checks passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:6094")
    run(parser.parse_args().base_url)
