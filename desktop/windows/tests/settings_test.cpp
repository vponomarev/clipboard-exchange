#include "settings.h"

#include <windows.h>

#include <iostream>
#include <string>

int main() {
    wchar_t current[MAX_PATH] = {};
    GetCurrentDirectoryW(ARRAYSIZE(current), current);
    const std::wstring directory = std::wstring(current) + L"\\settings-test";
    CreateDirectoryW(directory.c_str(), nullptr);
    SetEnvironmentVariableW(L"CLIPBOARD_EXCHANGE_SETTINGS_DIR", directory.c_str());
    SetEnvironmentVariableW(L"CLIPBOARD_EXCHANGE_DISABLE_STARTUP_WRITE", L"1");
    DeleteFileW((directory + L"\\settings.ini").c_str());

    Settings expected = DefaultSettings();
    expected.roomUrl = L"https://example.test/r/release#write=cw1_super-secret-value";
    std::wstring error;
    if (!SaveSettings(expected, &error)) {
        std::wcerr << L"save failed: " << error << L"\n";
        return 1;
    }
    const Settings actual = LoadSettings();
    if (actual.roomUrl != expected.roomUrl) {
        std::cerr << "DPAPI round trip changed room URL\n";
        return 1;
    }
    const std::wstring path = directory + L"\\settings.ini";
    HANDLE file = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING, 0, nullptr);
    if (file == INVALID_HANDLE_VALUE) {
        std::cerr << "cannot read settings file\n";
        return 1;
    }
    const DWORD size = GetFileSize(file, nullptr);
    std::string bytes(size, '\0');
    DWORD read = 0;
    ReadFile(file, &bytes[0], size, &read, nullptr);
    CloseHandle(file);
    bytes.resize(read);
    if (bytes.find("super-secret-value") != std::string::npos || bytes.find("example.test") != std::string::npos) {
        std::cerr << "settings file contains plaintext secret\n";
        return 1;
    }
    DeleteFileW((directory + L"\\settings.ini").c_str());
    RemoveDirectoryW(directory.c_str());
    std::cout << "DPAPI settings tests passed\n";
    return 0;
}
