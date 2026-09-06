#include "native_input.h"

#include <windows.h>

#include <iostream>
#include <string>

namespace {
struct ProviderSearch {
    DWORD processId;
    HWND window;
};

BOOL CALLBACK FindProviderWindow(HWND candidate, LPARAM parameter) {
    ProviderSearch* search = reinterpret_cast<ProviderSearch*>(parameter);
    DWORD processId = 0;
    GetWindowThreadProcessId(candidate, &processId);
    if (processId != search->processId) return TRUE;
    wchar_t className[128] = {};
    GetClassNameW(candidate, className, ARRAYSIZE(className));
    if (wcscmp(className, L"ClipboardExchangeSelectionProvider") != 0) return TRUE;
    search->window = candidate;
    return FALSE;
}

bool RunProvider(bool password, bool insertion, bool* result, std::wstring* text, std::wstring* error) {
    wchar_t executable[MAX_PATH] = {};
    GetModuleFileNameW(nullptr, executable, ARRAYSIZE(executable));
    std::wstring path(executable);
    path = path.substr(0, path.find_last_of(L"\\/") + 1) + L"selection-provider.exe";
    std::wstring command = L"\"" + path + L"\"" +
                           (password ? L" --password" : insertion ? L" --insert" : L"");
    STARTUPINFOW startup = {};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process = {};
    if (!CreateProcessW(path.c_str(), &command[0], nullptr, nullptr, FALSE, 0, nullptr, nullptr,
                        &startup, &process)) return false;
    HWND window = nullptr;
    const ULONGLONG deadline = GetTickCount64() + 5000;
    while (!window && GetTickCount64() < deadline) {
        ProviderSearch search = {process.dwProcessId, nullptr};
        EnumWindows(FindProviderWindow, reinterpret_cast<LPARAM>(&search));
        window = search.window;
        Sleep(20);
    }
    bool completed = false;
    if (window) {
        const DWORD providerThread = GetWindowThreadProcessId(window, nullptr);
        const DWORD currentThread = GetCurrentThreadId();
        HWND foreground = GetForegroundWindow();
        const DWORD foregroundThread = foreground ? GetWindowThreadProcessId(foreground, nullptr) : 0;
        if (foregroundThread && foregroundThread != currentThread)
            AttachThreadInput(currentThread, foregroundThread, TRUE);
        if (providerThread != currentThread) AttachThreadInput(currentThread, providerThread, TRUE);
        ShowWindow(window, SW_SHOWNORMAL);
        BringWindowToTop(window);
        SetForegroundWindow(window);
        HWND edit = GetWindow(window, GW_CHILD);
        SetActiveWindow(window);
        SetFocus(edit);
        GUITHREADINFO gui = {};
        gui.cbSize = sizeof(gui);
        GetGUIThreadInfo(providerThread, &gui);
        const bool focusEstablished = gui.hwndFocus == edit;
        Sleep(150);
        if (!focusEstablished) {
            if (error) *error = L"Smoke test could not focus the provider control";
            *result = false;
        } else if (insertion) {
            const std::wstring inserted = L"first line\nsecond line\r\nthird line";
            const std::wstring expected = L"first line\r\nsecond line\r\nthird line";
            *result = InsertUnicodeText(inserted, error);
            const ULONGLONG insertDeadline = GetTickCount64() + 2000;
            wchar_t value[256] = {};
            do {
                Sleep(20);
                SendMessageW(edit, WM_GETTEXT, ARRAYSIZE(value), reinterpret_cast<LPARAM>(value));
            } while (std::wstring(value) != expected && GetTickCount64() < insertDeadline);
            *text = value;
        } else {
            *result = ReadSelectedTextFromWindow(edit, text, error);
        }
        if (providerThread != currentThread) AttachThreadInput(currentThread, providerThread, FALSE);
        if (foregroundThread && foregroundThread != currentThread)
            AttachThreadInput(currentThread, foregroundThread, FALSE);
        completed = true;
        PostMessageW(window, WM_CLOSE, 0, 0);
    }
    if (WaitForSingleObject(process.hProcess, 5000) != WAIT_OBJECT_0) {
        TerminateProcess(process.hProcess, 1);
        WaitForSingleObject(process.hProcess, 1000);
        completed = false;
    }
    CloseHandle(process.hThread);
    CloseHandle(process.hProcess);
    return completed;
}
}

int wmain() {
    SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX);
    const HRESULT initialized = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    if (FAILED(initialized)) {
        std::cerr << "UI Automation COM initialization failed: 0x" << std::hex
                  << static_cast<unsigned int>(initialized) << "\n";
        return 1;
    }
    bool result = false;
    std::wstring text, error;
    const bool providerCompleted = RunProvider(false, false, &result, &text, &error);
    const bool selectedTextWasRead = result && text == L"selected text";
    const bool unsupportedWasSafe = !result && error.find(L"0x80040204") != std::wstring::npos;
    if (!providerCompleted || (!selectedTextWasRead && !unsupportedWasSafe)) {
        std::wcerr << L"UI Automation selection failed: " << error << L" value=" << text << L"\n";
        return 1;
    }
    text.clear(); error.clear(); result = true;
    if (!RunProvider(true, false, &result, &text, &error) || result ||
        error.find(L"пароля") == std::wstring::npos) {
        std::wcerr << L"password guard failed: " << error << L"\n";
        return 1;
    }
    text.clear(); error.clear(); result = false;
    if (!RunProvider(false, true, &result, &text, &error) || !result ||
        text != L"first line\r\nsecond line\r\nthird line") {
        std::cerr << "multiline insertion failed: " << std::string(error.begin(), error.end())
                  << " value=" << std::string(text.begin(), text.end()) << "\n";
        return 1;
    }
    CoUninitialize();
    std::cout << "native selection safety, password guard, and multiline insertion passed\n";
    return 0;
}
