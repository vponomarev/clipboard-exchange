#include <windows.h>

#include <iostream>
#include <string>

namespace {
bool Launch(const std::wstring& executable, const std::wstring& arguments, PROCESS_INFORMATION* process) {
    std::wstring command = L"\"" + executable + L"\" " + arguments;
    STARTUPINFOW startup = {};
    startup.cb = sizeof(startup);
    return CreateProcessW(executable.c_str(), &command[0], nullptr, nullptr, FALSE, 0,
                          nullptr, nullptr, &startup, process) != FALSE;
}

std::wstring ReadControlText(HWND control) {
    wchar_t value[512] = {};
    SendMessageW(control, WM_GETTEXT, ARRAYSIZE(value), reinterpret_cast<LPARAM>(value));
    return value;
}

bool EnterHotkey(HWND main, HWND control) {
    const DWORD targetThread = GetWindowThreadProcessId(main, nullptr);
    const DWORD ownThread = GetCurrentThreadId();
    AttachThreadInput(ownThread, targetThread, TRUE);
    SetForegroundWindow(main);
    SetFocus(control);
    AttachThreadInput(ownThread, targetThread, FALSE);
    Sleep(50);
    INPUT input[8] = {};
    const WORD keys[] = {VK_CONTROL, VK_SHIFT, VK_MENU, 'P', 'P', VK_MENU, VK_SHIFT, VK_CONTROL};
    for (int index = 0; index < 8; ++index) {
        input[index].type = INPUT_KEYBOARD;
        input[index].ki.wVk = keys[index];
        if (index >= 4) input[index].ki.dwFlags = KEYEVENTF_KEYUP;
    }
    return SendInput(ARRAYSIZE(input), input, sizeof(INPUT)) == ARRAYSIZE(input);
}
}

int main() {
    wchar_t ownPath[MAX_PATH] = {};
    GetModuleFileNameW(nullptr, ownPath, ARRAYSIZE(ownPath));
    std::wstring directory(ownPath);
    directory.resize(directory.find_last_of(L"\\/") + 1);
    const std::wstring application = directory + L"clipboard-exchange-win32.exe";
    PROCESS_INFORMATION first = {}, second = {};
    if (!Launch(application, L"--background", &first)) return 2;
    HWND main = nullptr;
    const ULONGLONG deadline = GetTickCount64() + 5000;
    while (!main && GetTickCount64() < deadline) {
        main = FindWindowW(L"ClipboardExchangeMainWindow", nullptr);
        Sleep(20);
    }
    if (!main || IsWindowVisible(main)) {
        std::cerr << "background instance did not start hidden\n";
        TerminateProcess(first.hProcess, 1);
        return 1;
    }
    for (int id = 110; id <= 113; ++id) {
        if (ReadControlText(GetDlgItem(main, id)).empty()) {
            std::cerr << "default hotkey field was not initialized\n";
            TerminateProcess(first.hProcess, 1);
            return 1;
        }
    }
    const wchar_t* deepLink = L"clipboard-exchange://connect?url=https%3A%2F%2Fexample.test%2Fr%2Frelease-room";
    if (!Launch(application, deepLink, &second)) return 2;
    if (WaitForSingleObject(second.hProcess, 4000) != WAIT_OBJECT_0) {
        std::cerr << "second instance did not hand off\n";
        TerminateProcess(second.hProcess, 1);
        TerminateProcess(first.hProcess, 1);
        return 1;
    }
    Sleep(150);
    const std::wstring room = ReadControlText(GetDlgItem(main, 100));
    if (!IsWindowVisible(main) || room != L"https://example.test/r/release-room") {
        std::wcerr << L"deep link handoff failed: " << room << L"\n";
        TerminateProcess(first.hProcess, 1);
        return 1;
    }
    HWND firstHotkey = GetDlgItem(main, 110);
    if (!EnterHotkey(main, firstHotkey)) {
        std::cerr << "could not inject hotkey input\n";
        TerminateProcess(first.hProcess, 1);
        return 1;
    }
    Sleep(100);
    if (ReadControlText(firstHotkey) != L"Ctrl+Shift+Alt+P") {
        std::wcerr << L"hotkey capture did not update the field: " << ReadControlText(firstHotkey) << L"\n";
        TerminateProcess(first.hProcess, 1);
        return 1;
    }
    PostMessageW(main, WM_COMMAND, 301, 0);
    const bool exited = WaitForSingleObject(first.hProcess, 4000) == WAIT_OBJECT_0;
    CloseHandle(first.hThread); CloseHandle(first.hProcess);
    CloseHandle(second.hThread); CloseHandle(second.hProcess);
    if (!exited) {
        std::cerr << "primary instance did not exit\n";
        return 1;
    }
    std::cout << "single-instance, defaults, hotkey capture, and deep-link handoff passed\n";
    return 0;
}
