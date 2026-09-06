#include <windows.h>

#ifdef _MSC_VER
#pragma comment(linker, "/manifestdependency:\"type='win32' name='Microsoft.Windows.Common-Controls' version='6.0.0.0' processorArchitecture='*' publicKeyToken='6595b64144ccf1df' language='*'\"")
#endif

namespace {
LRESULT CALLBACK Procedure(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    if (message == WM_CLOSE) { DestroyWindow(window); return 0; }
    if (message == WM_DESTROY) { PostQuitMessage(0); return 0; }
    return DefWindowProcW(window, message, wParam, lParam);
}
}

int WINAPI wWinMain(HINSTANCE instance, HINSTANCE, PWSTR commandLine, int) {
    WNDCLASSW type = {};
    type.hInstance = instance;
    type.lpfnWndProc = Procedure;
    type.lpszClassName = L"ClipboardExchangeSelectionProvider";
    type.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    RegisterClassW(&type);
    HWND window = CreateWindowW(type.lpszClassName, L"", WS_OVERLAPPEDWINDOW | WS_VISIBLE,
                                100, 100, 500, 160, nullptr, nullptr, instance, nullptr);
    const bool password = commandLine && wcsstr(commandLine, L"--password");
    const bool insertion = commandLine && wcsstr(commandLine, L"--insert");
    const DWORD editStyle = WS_CHILD | WS_VISIBLE |
        (password ? (ES_PASSWORD | ES_AUTOHSCROLL) : (ES_MULTILINE | ES_AUTOVSCROLL));
    HWND edit = CreateWindowExW(WS_EX_CLIENTEDGE, L"EDIT",
                                insertion ? L"" : L"prefix selected text suffix",
                                editStyle,
                                20, 20, 440, 80, window, nullptr, instance, nullptr);
    SetForegroundWindow(window);
    SetFocus(edit);
    if (!insertion) SendMessageW(edit, EM_SETSEL, 7, 20);
    MSG message;
    while (GetMessageW(&message, nullptr, 0, 0) > 0) {
        TranslateMessage(&message);
        DispatchMessageW(&message);
    }
    return 0;
}
