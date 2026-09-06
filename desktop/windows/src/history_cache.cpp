#include "history_cache.h"
#include "settings.h"

#include <windows.h>
#include <wincrypt.h>

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <vector>

namespace {
constexpr uint32_t kMagic = 0x31454843; // "CHE1"
constexpr size_t kMaxMessages = 50;
constexpr size_t kMaxSerializedBytes = 8 * 1024 * 1024;

std::wstring CachePath() {
    std::wstring path = SettingsPath();
    const size_t slash = path.find_last_of(L"\\/");
    return path.substr(0, slash + 1) + L"history.cache";
}

template <typename T> void Append(std::vector<BYTE>* data, const T& value) {
    const BYTE* bytes = reinterpret_cast<const BYTE*>(&value);
    data->insert(data->end(), bytes, bytes + sizeof(value));
}

void AppendString(std::vector<BYTE>* data, const std::wstring& value) {
    const uint32_t length = static_cast<uint32_t>(value.size());
    Append(data, length);
    const BYTE* bytes = reinterpret_cast<const BYTE*>(value.data());
    data->insert(data->end(), bytes, bytes + value.size() * sizeof(wchar_t));
}

template <typename T> bool Read(const std::vector<BYTE>& data, size_t* offset, T* value) {
    if (*offset > data.size() || data.size() - *offset < sizeof(T)) return false;
    std::memcpy(value, data.data() + *offset, sizeof(T));
    *offset += sizeof(T);
    return true;
}

bool ReadString(const std::vector<BYTE>& data, size_t* offset, std::wstring* value) {
    uint32_t length = 0;
    if (!Read(data, offset, &length) || length > kMaxSerializedBytes / sizeof(wchar_t)) return false;
    const size_t bytes = static_cast<size_t>(length) * sizeof(wchar_t);
    if (*offset > data.size() || data.size() - *offset < bytes) return false;
    value->assign(reinterpret_cast<const wchar_t*>(data.data() + *offset), length);
    *offset += bytes;
    return true;
}

DATA_BLOB Entropy() {
    static wchar_t purpose[] = L"ClipboardExchange.Win32.History.v1";
    return {sizeof(purpose), reinterpret_cast<BYTE*>(purpose)};
}
}

bool SaveHistoryCache(const std::wstring& roomUrl, const std::vector<std::wstring>& messages) {
    if (roomUrl.empty()) return false;
    std::vector<BYTE> plain;
    Append(&plain, kMagic);
    AppendString(&plain, roomUrl);
    const uint32_t count = static_cast<uint32_t>(std::min(messages.size(), kMaxMessages));
    Append(&plain, count);
    for (size_t index = 0; index < count; ++index) AppendString(&plain, messages[index]);
    if (plain.size() > kMaxSerializedBytes) return false;

    DATA_BLOB input = {static_cast<DWORD>(plain.size()), plain.data()};
    DATA_BLOB entropy = Entropy();
    DATA_BLOB cipher = {};
    const bool protectedOk = CryptProtectData(&input, L"Clipboard Exchange history", &entropy,
                                               nullptr, nullptr, CRYPTPROTECT_UI_FORBIDDEN,
                                               &cipher) != FALSE;
    SecureZeroMemory(plain.data(), plain.size());
    if (!protectedOk) return false;

    const std::wstring path = CachePath();
    const std::wstring temporary = path + L".tmp";
    HANDLE file = CreateFileW(temporary.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS,
                              FILE_ATTRIBUTE_HIDDEN | FILE_ATTRIBUTE_TEMPORARY, nullptr);
    DWORD written = 0;
    const bool wrote = file != INVALID_HANDLE_VALUE &&
        WriteFile(file, cipher.pbData, cipher.cbData, &written, nullptr) && written == cipher.cbData &&
        FlushFileBuffers(file);
    if (file != INVALID_HANDLE_VALUE) CloseHandle(file);
    LocalFree(cipher.pbData);
    if (!wrote) { DeleteFileW(temporary.c_str()); return false; }
    if (!MoveFileExW(temporary.c_str(), path.c_str(), MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)) {
        DeleteFileW(temporary.c_str());
        return false;
    }
    return true;
}

std::vector<std::wstring> LoadHistoryCache(const std::wstring& roomUrl) {
    std::vector<std::wstring> empty;
    if (roomUrl.empty()) return empty;
    HANDLE file = CreateFileW(CachePath().c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr,
                              OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) return empty;
    LARGE_INTEGER size = {};
    if (!GetFileSizeEx(file, &size) || size.QuadPart <= 0 ||
        size.QuadPart > static_cast<LONGLONG>(kMaxSerializedBytes)) {
        CloseHandle(file);
        return empty;
    }
    std::vector<BYTE> cipher(static_cast<size_t>(size.QuadPart));
    DWORD read = 0;
    const bool readOk = ReadFile(file, cipher.data(), static_cast<DWORD>(cipher.size()), &read, nullptr) &&
                        read == cipher.size();
    CloseHandle(file);
    if (!readOk) return empty;

    DATA_BLOB input = {static_cast<DWORD>(cipher.size()), cipher.data()};
    DATA_BLOB entropy = Entropy();
    DATA_BLOB output = {};
    if (!CryptUnprotectData(&input, nullptr, &entropy, nullptr, nullptr,
                            CRYPTPROTECT_UI_FORBIDDEN, &output)) return empty;
    std::vector<BYTE> plain(output.pbData, output.pbData + output.cbData);
    SecureZeroMemory(output.pbData, output.cbData);
    LocalFree(output.pbData);
    size_t offset = 0;
    uint32_t magic = 0, count = 0;
    std::wstring cachedRoom;
    std::vector<std::wstring> messages;
    bool valid = Read(plain, &offset, &magic) && magic == kMagic &&
                 ReadString(plain, &offset, &cachedRoom) && cachedRoom == roomUrl &&
                 Read(plain, &offset, &count) && count <= kMaxMessages;
    for (uint32_t index = 0; valid && index < count; ++index) {
        std::wstring message;
        valid = ReadString(plain, &offset, &message);
        if (valid) messages.push_back(message);
    }
    valid = valid && offset == plain.size();
    SecureZeroMemory(plain.data(), plain.size());
    return valid ? messages : empty;
}
