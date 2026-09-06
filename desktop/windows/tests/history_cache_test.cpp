#include "history_cache.h"

#include <windows.h>

#include <iostream>
#include <string>
#include <vector>

int main() {
    wchar_t current[MAX_PATH] = {};
    GetCurrentDirectoryW(ARRAYSIZE(current), current);
    const std::wstring directory = std::wstring(current) + L"\\history-test";
    CreateDirectoryW(directory.c_str(), nullptr);
    SetEnvironmentVariableW(L"CLIPBOARD_EXCHANGE_SETTINGS_DIR", directory.c_str());
    const std::wstring cache = directory + L"\\history.cache";
    DeleteFileW(cache.c_str());

    const std::wstring room = L"https://example.test/r/private#write=secret";
    const std::vector<std::wstring> expected = {L"one", L"Привет\n世界", L"last"};
    if (!SaveHistoryCache(room, expected)) {
        std::cerr << "cannot save history cache\n";
        return 1;
    }
    if (LoadHistoryCache(room) != expected) {
        std::cerr << "history cache round trip failed\n";
        return 1;
    }
    std::vector<std::wstring> newestFirst;
    for (int index = 0; index < 60; ++index) newestFirst.push_back(L"message-" + std::to_wstring(index));
    if (!SaveHistoryCache(room, newestFirst)) {
        std::cerr << "cannot save oversized history cache\n";
        return 1;
    }
    const std::vector<std::wstring> limited = LoadHistoryCache(room);
    if (limited.size() != 50 || limited.front() != L"message-0" || limited.back() != L"message-49") {
        std::cerr << "history cache did not preserve the newest-first order\n";
        return 1;
    }
    if (!LoadHistoryCache(L"https://example.test/r/other").empty()) {
        std::cerr << "cache was accepted for another room\n";
        return 1;
    }
    HANDLE file = CreateFileW(cache.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr,
                              OPEN_EXISTING, 0, nullptr);
    const DWORD size = GetFileSize(file, nullptr);
    std::string bytes(size, '\0');
    DWORD read = 0;
    ReadFile(file, &bytes[0], size, &read, nullptr);
    CloseHandle(file);
    if (bytes.find("secret") != std::string::npos || bytes.find("example.test") != std::string::npos) {
        std::cerr << "history cache leaks plaintext\n";
        return 1;
    }
    DeleteFileW(cache.c_str());
    RemoveDirectoryW(directory.c_str());
    std::cout << "DPAPI history cache tests passed\n";
    return 0;
}
