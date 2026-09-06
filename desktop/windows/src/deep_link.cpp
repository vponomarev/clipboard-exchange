#include "deep_link.h"

#include "room_client.h"

#include <windows.h>
#include <shellapi.h>

#include <cstdlib>
#include <vector>

namespace {
std::wstring DecodePercentUtf8(const std::wstring& value) {
    std::string bytes;
    for (size_t index = 0; index < value.size(); ++index) {
        if (value[index] == L'%' && index + 2 < value.size()) {
            wchar_t encoded[3] = {value[index + 1], value[index + 2], 0};
            wchar_t* end = nullptr;
            const long byte = wcstol(encoded, &end, 16);
            if (end == encoded + 2) {
                bytes += static_cast<char>(byte);
                index += 2;
                continue;
            }
        }
        if (value[index] == L'+') bytes += ' ';
        else if (value[index] <= 0x7f) bytes += static_cast<char>(value[index]);
        else return {};
    }
    if (bytes.empty()) return {};
    const int count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, bytes.data(),
                                          static_cast<int>(bytes.size()), nullptr, 0);
    if (!count) return {};
    std::wstring decoded(static_cast<size_t>(count), L'\0');
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, bytes.data(),
                        static_cast<int>(bytes.size()), &decoded[0], count);
    return decoded;
}

std::vector<std::wstring> ParseArguments(const std::wstring& commandLine) {
    const std::wstring full = L"clipboard-exchange.exe " + commandLine;
    int count = 0;
    LPWSTR* values = CommandLineToArgvW(full.c_str(), &count);
    std::vector<std::wstring> result;
    if (values) {
        for (int index = 1; index < count; ++index) result.push_back(values[index]);
        LocalFree(values);
    }
    return result;
}
}

std::wstring ExtractRoomArgument(const std::wstring& commandLine) {
    std::wstring target;
    const std::vector<std::wstring> arguments = ParseArguments(commandLine);
    for (const std::wstring& argument : arguments) {
        if (argument.compare(0, 7, L"http://") == 0 ||
            argument.compare(0, 8, L"https://") == 0) {
            target = argument;
        } else if (argument.compare(0, 19, L"clipboard-exchange:") == 0) {
            const size_t query = argument.find(L'?');
            size_t start = query == std::wstring::npos ? std::wstring::npos : query + 1;
            while (start != std::wstring::npos && start <= argument.size()) {
                const size_t end = argument.find(L'&', start);
                const std::wstring field = argument.substr(start, end == std::wstring::npos
                    ? std::wstring::npos : end - start);
                if (field.compare(0, 4, L"url=") == 0) {
                    target = DecodePercentUtf8(field.substr(4));
                    break;
                }
                start = end == std::wstring::npos ? std::wstring::npos : end + 1;
            }
        }
        if (!target.empty()) break;
    }
    std::wstring error;
    return !target.empty() && ValidateRoomUrl(target, &error) ? target : std::wstring();
}
