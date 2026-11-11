import argparse
from playwright.sync_api import sync_playwright, expect


PAGES = [
    ("overview", "运行概览"),
    ("context", "当前上下文"),
    ("messages", "消息记录"),
    ("activity", "活动日志"),
    ("agent", "Agent 管理"),
    ("qq", "QQ 管理"),
    ("config", "配置"),
]


def login(page):
    page.get_by_label("访问令牌", exact=True).fill("wrong")
    page.get_by_role("button", name="进入控制台", exact=True).click()
    expect(page.get_by_role("alert")).to_contain_text("不正确")
    page.get_by_label("访问令牌", exact=True).fill("preview-token")
    page.get_by_role("button", name="进入控制台", exact=True).click()
    page.get_by_role("navigation", name="主导航").wait_for()


def check_page_matrix(page):
    for width, height in ((1440, 900), (1024, 768), (390, 844)):
        page.set_viewport_size({"width": width, "height": height})
        for theme in ("dark", "light"):
            actual = page.locator("body").get_attribute("data-theme")
            if actual != theme:
                if width <= 760:
                    page.get_by_role("button", name="打开导航").click()
                page.get_by_role("button", name="切换主题").click()
                if width <= 760:
                    page.get_by_role("button", name="关闭导航").click()
            expect(page.locator("body")).to_have_attribute("data-theme", theme)
            for route, title in PAGES:
                if width <= 760:
                    page.get_by_role("button", name="打开导航").click()
                page.get_by_role("navigation", name="主导航").get_by_role(
                    "button", name=title, exact=True
                ).click()
                expect(page.get_by_role("heading", name=title, exact=True)).to_be_visible()
                assert page.locator("html").evaluate(
                    "(el) => el.scrollWidth <= window.innerWidth"
                ), f"horizontal overflow at {width}px, {theme}, {route}"
            if width == 1440:
                page.screenshot(path=f".tools/webui-{theme}.png", full_page=True)


def check_context_detail_and_conflict(page):
    nav = page.get_by_role("navigation", name="主导航")
    nav.get_by_role("button", name="当前上下文", exact=True).click()
    row = page.locator(".context-row").filter(has_text="<script>").first
    expect(row).to_be_visible()
    row.click()
    dialog = page.get_by_role("dialog", name="上下文详情")
    expect(dialog).to_be_visible()
    expect(dialog.locator("pre").first).to_contain_text("<script>")
    assert page.evaluate("window.injected") is None
    page.get_by_role("button", name="关闭详情").click()
    expect(dialog).not_to_be_visible()
    page.route(
        "**/api/context/message?*",
        lambda route: route.fulfill(
            status=409,
            content_type="application/json",
            body='{"error":"上下文版本已更新，请刷新列表","code":"version_conflict"}',
        ),
    )
    page.locator(".context-row").first.click()
    expect(page.locator('[x-show="contextConflict"]')).to_contain_text("版本已更新")
    assert page.evaluate("window.injected") is None


def check_config_drafts(page):
    nav = page.get_by_role("navigation", name="主导航")
    nav.get_by_role("button", name="配置", exact=True).click()
    page.get_by_role("tab", name="行为与上下文").click()
    page.get_by_label("温度", exact=True).fill("0.4")
    page.get_by_role("tab", name="模型接入").click()
    page.get_by_role("tab", name="行为与上下文").click()
    expect(page.get_by_label("温度", exact=True)).to_have_value("0.4")
    assert page.get_by_role("button", name="检查并保存").is_enabled()


def check_dangerous_action_requires_confirmation(page):
    nav = page.get_by_role("navigation", name="主导航")
    nav.get_by_role("button", name="QQ 管理", exact=True).click()
    page.get_by_role("tab", name="高级动作").click()
    action = page.locator(".advanced-section select")
    action.select_option("set_group_kick")
    confirm = page.locator(".advanced-section input[type=checkbox]")
    expect(confirm).not_to_be_checked()
    expect(page.get_by_role("button", name="执行动作")).to_be_disabled()
    action.select_option("get_status")
    action.select_option("set_group_kick")
    expect(confirm).not_to_be_checked()
    expect(page.get_by_role("button", name="执行动作")).to_be_disabled()


def run(base_url, smoke=False, matrix=False):
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True, channel="chrome")
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(base_url)
        page.wait_for_load_state("networkidle")
        login(page)
        if matrix:
            check_page_matrix(page)
            page.set_viewport_size({"width": 1440, "height": 900})
            check_context_detail_and_conflict(page)
            check_dangerous_action_requires_confirmation(page)
            check_config_drafts(page)
        else:
            page.get_by_role("navigation", name="主导航").get_by_role(
                "button", name="当前上下文", exact=True
            ).click()
        expect(page).to_have_url(base_url.rstrip("/") + ("/#config" if matrix else "/#context"))
        assert page.locator("html").evaluate("(el) => el.scrollWidth <= innerWidth")
        assert not errors, errors
        browser.close()
    print("Browser matrix passed" if matrix else "Browser smoke passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:6088")
    parser.add_argument("--smoke", action="store_true")
    parser.add_argument("--matrix", action="store_true")
    args = parser.parse_args()
    run(args.base_url, args.smoke, args.matrix)
