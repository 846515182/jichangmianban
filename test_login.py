from playwright.sync_api import sync_playwright
import time

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    context = browser.new_context(
        viewport={'width': 1280, 'height': 800},
        user_agent='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
    )
    page = context.new_page()

    # 监听 console
    page.on('console', lambda msg: print(f'[console] {msg.type}: {msg.text}'))
    page.on('pageerror', lambda err: print(f'[pageerror] {err}'))
    page.on('response', lambda resp: print(f'[response] {resp.status} {resp.url}') if '/api/v1/auth/login' in resp.url else None)

    print('=== 1. 打开登录页 ===')
    page.goto('https://bbcdtv.top/login', wait_until='networkidle')
    page.screenshot(path='/workspace/login_before.png', full_page=True)
    print('URL:', page.url)
    print('标题:', page.title())

    inputs = page.locator('input').all()
    print('输入框数量:', len(inputs))
    for inp in inputs:
        print('  - placeholder:', inp.get_attribute('placeholder'), 'name:', inp.get_attribute('name'))

    buttons = page.locator('button').all()
    print('按钮数量:', len(buttons))
    for btn in buttons:
        if btn.is_visible():
            print('  - button text:', btn.inner_text().strip())

    print('\n=== 2. 填写账号密码 ===')
    page.locator('input[type="text"], input[name="username"], input[placeholder*="用户名"]').fill('admin')
    page.locator('input[type="password"], input[name="password"], input[placeholder*="密码"]').fill('Admin@2024!')
    page.screenshot(path='/workspace/login_filled.png', full_page=True)

    print('=== 3. 点击登录 ===')
    page.locator('button[type="submit"], button:has-text("登录"), button:has-text("Login")').click()

    time.sleep(4)
    page.screenshot(path='/workspace/login_after.png', full_page=True)
    print('登录后 URL:', page.url)
    print('登录后标题:', page.title())

    error_el = page.locator('.el-message--error, .error, .text-red, [role="alert"]').first
    try:
        if error_el.is_visible():
            print('错误提示:', error_el.inner_text().strip())
    except Exception as e:
        print('无可见错误提示')

    browser.close()
