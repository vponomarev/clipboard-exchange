#include "room_client.h"
#include "crypto.h"

#include <windows.h>
#include <winhttp.h>
#include <objbase.h>

#include <cstdio>
#include <string>

namespace {
struct Target {
    bool secure = false;
    std::wstring host;
    INTERNET_PORT port = 0;
    std::wstring room;
    std::wstring writeToken;
    std::vector<unsigned char> key;
};

struct InternetHandle {
    HINTERNET value = nullptr;
    ~InternetHandle() { if (value) WinHttpCloseHandle(value); }
};

bool ValidCapability(const std::wstring& value, const wchar_t* prefix) {
    const size_t prefixLength = wcslen(prefix);
    if (value.size() != prefixLength + 43 || value.compare(0, prefixLength, prefix) != 0)
        return false;
    for (size_t index = prefixLength; index < value.size(); ++index) {
        const wchar_t character = value[index];
        if (!((character >= L'A' && character <= L'Z') ||
              (character >= L'a' && character <= L'z') ||
              (character >= L'0' && character <= L'9') || character == L'-' || character == L'_'))
            return false;
    }
    return true;
}

std::string Utf8(const std::wstring& value) {
    if (value.empty()) return {};
    const int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()),
                                         nullptr, 0, nullptr, nullptr);
    std::string output(static_cast<size_t>(size), '\0');
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()),
                        &output[0], size, nullptr, nullptr);
    return output;
}

std::wstring Wide(const std::string& value) {
    if (value.empty()) return {};
    const int size = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                                         static_cast<int>(value.size()), nullptr, 0);
    if (!size) return {};
    std::wstring output(static_cast<size_t>(size), L'\0');
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                        static_cast<int>(value.size()), &output[0], size);
    return output;
}

std::wstring WinError(const wchar_t* prefix, DWORD code = GetLastError()) {
    wchar_t* system = nullptr;
    FormatMessageW(FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM |
                   FORMAT_MESSAGE_IGNORE_INSERTS, nullptr, code, 0,
                   reinterpret_cast<wchar_t*>(&system), 0, nullptr);
    std::wstring result(prefix);
    if (system) {
        result += L": ";
        result += system;
        while (!result.empty() && (result.back() == L'\r' || result.back() == L'\n')) result.pop_back();
        LocalFree(system);
    }
    return result;
}

bool ParseTarget(const std::wstring& input, Target* target, std::wstring* error) {
    if (!target) return false;
    std::wstring url = input;
    while (!url.empty() && iswspace(url.front())) url.erase(url.begin());
    while (!url.empty() && iswspace(url.back())) url.pop_back();
    const size_t fragmentAt = url.find(L'#');
    std::wstring fragment;
    if (fragmentAt != std::wstring::npos) {
        fragment = url.substr(fragmentAt + 1);
        url.erase(fragmentAt);
    }
    URL_COMPONENTSW components = {};
    components.dwStructSize = sizeof(components);
    components.dwHostNameLength = static_cast<DWORD>(-1);
    components.dwUrlPathLength = static_cast<DWORD>(-1);
    components.dwUserNameLength = static_cast<DWORD>(-1);
    components.dwPasswordLength = static_cast<DWORD>(-1);
    if (!WinHttpCrackUrl(url.c_str(), static_cast<DWORD>(url.size()), 0, &components) ||
        (components.nScheme != INTERNET_SCHEME_HTTP && components.nScheme != INTERNET_SCHEME_HTTPS)) {
        if (error) *error = L"Нужна полная HTTP/HTTPS-ссылка комнаты";
        return false;
    }
    if (components.dwUserNameLength || components.dwPasswordLength) {
        if (error) *error = L"Ссылка не должна содержать логин или пароль";
        return false;
    }
    const std::wstring path(components.lpszUrlPath, components.dwUrlPathLength);
    const std::wstring prefix = L"/r/";
    if (path.compare(0, prefix.size(), prefix) != 0 || path.size() <= prefix.size()) {
        if (error) *error = L"Ссылка должна вести в комнату /r/...";
        return false;
    }
    std::wstring room = path.substr(prefix.size());
    if (!room.empty() && room.back() == L'/') room.pop_back();
    if (room.empty() || room.find(L'/') != std::wstring::npos || room.size() > 64) {
        if (error) *error = L"Некорректный идентификатор комнаты";
        return false;
    }
    for (wchar_t character : room) {
        if (!((character >= L'A' && character <= L'Z') || (character >= L'a' && character <= L'z') ||
              (character >= L'0' && character <= L'9') || character == L'_' || character == L'-')) {
            if (error) *error = L"Некорректный идентификатор комнаты";
            return false;
        }
    }
    const auto fragmentValue = [&fragment](const std::wstring& name) {
        size_t start = 0;
        while (start <= fragment.size()) {
            const size_t end = fragment.find(L'&', start);
            const std::wstring field = fragment.substr(start, end == std::wstring::npos
                ? std::wstring::npos : end - start);
            const size_t equals = field.find(L'=');
            if (equals != std::wstring::npos && field.substr(0, equals) == name) return field.substr(equals + 1);
            if (end == std::wstring::npos) break;
            start = end + 1;
        }
        return std::wstring();
    };
    const std::wstring writeToken = fragmentValue(L"write");
    const std::wstring keyText = fragmentValue(L"key");
    if (!writeToken.empty() && !ValidCapability(writeToken, L"cw1_")) {
        if (error) *error = L"Некорректная write capability комнаты";
        return false;
    }
    std::vector<unsigned char> key;
    if (!keyText.empty() && (!ValidCapability(keyText, L"ce1_") ||
        !Base64UrlDecode(keyText.substr(4), &key) || key.size() != 32)) {
        if (error) *error = L"Некорректный ключ шифрованной комнаты";
        return false;
    }
    target->secure = components.nScheme == INTERNET_SCHEME_HTTPS;
    target->host.assign(components.lpszHostName, components.dwHostNameLength);
    target->port = components.nPort;
    target->room = room;
    target->writeToken = writeToken;
    target->key.swap(key);
    return true;
}

bool Request(const Target& target, const wchar_t* method, const std::wstring& path,
             const std::string& body, bool authorize, std::string* response,
             DWORD* status, std::wstring* error) {
    InternetHandle session, connection, request;
    session.value = WinHttpOpen(L"ClipboardExchangeWin32/0.1", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY,
                                WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!session.value) { if (error) *error = WinError(L"WinHTTP недоступен"); return false; }
    WinHttpSetTimeouts(session.value, 5000, 5000, 10000, 10000);
    connection.value = WinHttpConnect(session.value, target.host.c_str(), target.port, 0);
    if (!connection.value) { if (error) *error = WinError(L"Не удалось подключиться"); return false; }
    request.value = WinHttpOpenRequest(connection.value, method, path.c_str(), nullptr,
                                       WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES,
                                       target.secure ? WINHTTP_FLAG_SECURE : 0);
    if (!request.value) { if (error) *error = WinError(L"Не удалось создать HTTP-запрос"); return false; }
    std::wstring headers;
    if (!body.empty()) headers = L"Content-Type: application/json\r\n";
    if (authorize && !target.writeToken.empty()) headers += L"Authorization: ClipboardWrite " + target.writeToken + L"\r\n";
    if (body.size() > MAXDWORD) {
        if (error) *error = L"Запрос превышает допустимый размер";
        return false;
    }
    const BOOL sent = WinHttpSendRequest(request.value, headers.empty() ? WINHTTP_NO_ADDITIONAL_HEADERS : headers.c_str(),
                                         headers.empty() ? 0 : static_cast<DWORD>(-1),
                                         body.empty() ? WINHTTP_NO_REQUEST_DATA : const_cast<char*>(body.data()),
                                         static_cast<DWORD>(body.size()), static_cast<DWORD>(body.size()), 0);
    if (!sent || !WinHttpReceiveResponse(request.value, nullptr)) {
        if (error) *error = WinError(L"Ошибка HTTP-запроса");
        return false;
    }
    DWORD code = 0, codeSize = sizeof(code);
    WinHttpQueryHeaders(request.value, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER,
                        WINHTTP_HEADER_NAME_BY_INDEX, &code, &codeSize, WINHTTP_NO_HEADER_INDEX);
    if (status) *status = code;
    if (response) {
        constexpr size_t kMaxResponseBytes = 16 * 1024 * 1024;
        response->clear();
        for (;;) {
            DWORD available = 0;
            if (!WinHttpQueryDataAvailable(request.value, &available)) {
                if (error) *error = WinError(L"Не удалось прочитать HTTP-ответ");
                return false;
            }
            if (!available) break;
            if (available > kMaxResponseBytes - response->size()) {
                if (error) *error = L"Ответ сервера превышает допустимый размер";
                return false;
            }
            const size_t start = response->size();
            response->resize(start + available);
            DWORD read = 0;
            if (!WinHttpReadData(request.value, &(*response)[start], available, &read)) {
                if (error) *error = WinError(L"Не удалось прочитать HTTP-ответ");
                return false;
            }
            response->resize(start + read);
        }
    }
    if (code < 200 || code >= 300) {
        if (error) {
            if (code == 400) *error = L"Сервер отклонил некорректный запрос";
            else if (code == 403) *error = L"Комната доступна только для чтения или write capability недействителен";
            else if (code == 404) *error = L"Комната не найдена";
            else if (code == 413) *error = L"Сообщение превышает лимит сервера";
            else if (code == 429) *error = L"Слишком много запросов; повторите позже";
            else {
                wchar_t value[80];
                wsprintfW(value, L"Сервер вернул HTTP %lu", code);
                *error = value;
            }
        }
        return false;
    }
    return true;
}

size_t SkipString(const std::string& json, size_t at) {
    for (size_t index = at + 1; index < json.size(); ++index) {
        if (json[index] == '\\') ++index;
        else if (json[index] == '"') return index + 1;
    }
    return json.size();
}

bool Hex(char value, unsigned* result) {
    if (value >= '0' && value <= '9') *result = value - '0';
    else if (value >= 'a' && value <= 'f') *result = value - 'a' + 10;
    else if (value >= 'A' && value <= 'F') *result = value - 'A' + 10;
    else return false;
    return true;
}

std::wstring DecodeString(const std::string& json, size_t quote, size_t* after) {
    std::wstring output;
    std::string utf8;
    const auto flush = [&]() {
        output += Wide(utf8);
        utf8.clear();
    };
    size_t index = quote + 1;
    for (; index < json.size() && json[index] != '"'; ++index) {
        if (json[index] != '\\') { utf8 += json[index]; continue; }
        flush();
        if (++index >= json.size()) break;
        const char escaped = json[index];
        if (escaped == 'n') output += L'\n';
        else if (escaped == 'r') output += L'\r';
        else if (escaped == 't') output += L'\t';
        else if (escaped == 'b') output += L'\b';
        else if (escaped == 'f') output += L'\f';
        else if (escaped == 'u' && index + 4 < json.size()) {
            unsigned code = 0, digit = 0;
            bool valid = true;
            for (int offset = 1; offset <= 4; ++offset) {
                if (!Hex(json[index + offset], &digit)) { valid = false; break; }
                code = code * 16 + digit;
            }
            if (valid) { output += static_cast<wchar_t>(code); index += 4; }
        } else output += static_cast<wchar_t>(static_cast<unsigned char>(escaped));
    }
    flush();
    if (after) *after = index < json.size() ? index + 1 : index;
    return output;
}

bool StringField(const std::string& object, const char* name, std::wstring* value) {
    const std::string marker = std::string("\"") + name + "\"";
    size_t at = object.find(marker);
    if (at == std::string::npos) return false;
    at = object.find(':', at + marker.size());
    if (at == std::string::npos) return false;
    at = object.find('"', at + 1);
    if (at == std::string::npos) return false;
    *value = DecodeString(object, at, nullptr);
    return true;
}

int IntegerField(const std::string& object, const char* name, int fallback) {
    const std::string marker = std::string("\"") + name + "\"";
    size_t at = object.find(marker);
    if (at == std::string::npos || (at = object.find(':', at + marker.size())) == std::string::npos) return fallback;
    return std::atoi(object.c_str() + at + 1);
}

bool ParseSnapshot(const std::string& json, const Target& target, RoomSnapshot* snapshot, std::wstring* error) {
    snapshot->encrypted = json.find("\"encrypted\":true") != std::string::npos;
    snapshot->writeProtected = json.find("\"writeProtected\":true") != std::string::npos;
    snapshot->canWrite = !snapshot->writeProtected || !target.writeToken.empty();
    StringField(json, "keyId", &snapshot->keyId);
    if (snapshot->encrypted) {
        if (target.key.size() != 32) { if (error) *error = L"В ссылке нет ключа шифрованной комнаты"; return false; }
        std::vector<unsigned char> digest;
        if (!Sha256(target.key, &digest) || Base64UrlEncode(digest) != snapshot->keyId) {
            if (error) *error = L"Ключ не подходит к этой комнате";
            return false;
        }
    }
    snapshot->messages.clear();
    const size_t itemsName = json.find("\"items\"");
    size_t index = itemsName == std::string::npos ? std::string::npos : json.find('[', itemsName);
    if (index == std::string::npos) { if (error) *error = L"Некорректный ответ сервера"; return false; }
    ++index;
    while (index < json.size()) {
        while (index < json.size() && (json[index] == ' ' || json[index] == '\r' || json[index] == '\n' || json[index] == ',')) ++index;
        if (index >= json.size() || json[index] == ']') break;
        if (json[index] != '{') { ++index; continue; }
        const size_t start = index;
        int depth = 0;
        for (; index < json.size(); ++index) {
            if (json[index] == '"') { index = SkipString(json, index) - 1; continue; }
            if (json[index] == '{') ++depth;
            else if (json[index] == '}' && --depth == 0) { ++index; break; }
        }
        const std::string object = json.substr(start, index - start);
        std::wstring kind, content;
        if (!StringField(object, "kind", &kind)) continue;
        if (kind == L"text" && StringField(object, "content", &content)) {
            snapshot->messages.push_back(content);
        } else if (kind == L"encrypted" && snapshot->encrypted) {
            std::wstring cipherText, ivText, itemKeyId;
            const int version = IntegerField(object, "version", 1);
            if ((version != 1 && version != 2) || !StringField(object, "ciphertext", &cipherText) ||
                !StringField(object, "iv", &ivText) || !StringField(object, "keyId", &itemKeyId) ||
                itemKeyId != snapshot->keyId) {
                if (error) *error = L"Некорректное шифрованное сообщение";
                return false;
            }
            std::vector<unsigned char> cipher, iv, plain;
            const std::string aadText = "clipboard-exchange:v" + std::to_string(version) + ":" + Utf8(target.room);
            if (!Base64UrlDecode(cipherText, &cipher) || !Base64UrlDecode(ivText, &iv) ||
                !Aes256GcmDecrypt(target.key, iv, std::vector<unsigned char>(aadText.begin(), aadText.end()),
                                  cipher, &plain)) {
                if (error) *error = L"Не удалось расшифровать сообщение";
                return false;
            }
            const std::string payload(plain.begin(), plain.end());
            if (!StringField(payload, "text", &content)) {
                if (error) *error = L"Некорректные данные шифрованного сообщения";
                return false;
            }
            snapshot->messages.push_back(content);
        }
    }
    return true;
}

std::string JsonEscape(const std::wstring& value) {
    std::string output;
    // Convert UTF-16 as a whole so surrogate pairs remain one Unicode scalar.
    for (unsigned char character : Utf8(value)) {
        if (character == L'"') output += "\\\"";
        else if (character == L'\\') output += "\\\\";
        else if (character == L'\n') output += "\\n";
        else if (character == L'\r') output += "\\r";
        else if (character == L'\t') output += "\\t";
        else if (character < 0x20) {
            char encoded[7];
            std::snprintf(encoded, sizeof(encoded), "\\u%04x", static_cast<unsigned>(character));
            output += encoded;
        } else output += static_cast<char>(character);
    }
    return output;
}

std::wstring GuidText() {
    GUID id;
    if (FAILED(CoCreateGuid(&id))) return {};
    wchar_t value[40];
    StringFromGUID2(id, value, 40);
    std::wstring result(value + 1);
    result.pop_back();
    return result;
}
}

bool ValidateRoomUrl(const std::wstring& roomUrl, std::wstring* error) {
    Target target;
    return ParseTarget(roomUrl, &target, error);
}

bool FetchRoom(const std::wstring& roomUrl, RoomSnapshot* snapshot, std::wstring* error) {
    Target target;
    if (!ParseTarget(roomUrl, &target, error)) return false;
    std::string response;
    DWORD status = 0;
    if (!Request(target, L"GET", L"/api/rooms/" + target.room + L"/history?limit=30", {}, false, &response, &status, error)) return false;
    return ParseSnapshot(response, target, snapshot, error);
}

bool SendRoomText(const std::wstring& roomUrl, const std::wstring& text, std::wstring* error) {
    if (text.empty()) { if (error) *error = L"Нельзя отправить пустой текст"; return false; }
    Target target;
    if (!ParseTarget(roomUrl, &target, error)) return false;
    RoomSnapshot snapshot;
    std::string metadata;
    DWORD metadataStatus = 0;
    if (!Request(target, L"GET", L"/api/rooms/" + target.room + L"/history?limit=0", {}, false, &metadata, &metadataStatus, error) ||
        !ParseSnapshot(metadata, target, &snapshot, error)) return false;
    const std::wstring id = GuidText();
    if (id.empty()) { if (error) *error = L"Не удалось создать ID сообщения"; return false; }
    std::string body;
    if (snapshot.encrypted) {
        std::vector<unsigned char> iv, cipher;
        const std::string payload = "{\"text\":\"" + JsonEscape(text) + "\",\"alias\":\"\"}";
        const std::string aadText = "clipboard-exchange:v2:" + Utf8(target.room);
        if (!RandomBytes(12, &iv) ||
            !Aes256GcmEncrypt(target.key, iv, std::vector<unsigned char>(aadText.begin(), aadText.end()),
                              std::vector<unsigned char>(payload.begin(), payload.end()), &cipher)) {
            if (error) *error = L"Windows BCrypt не смог зашифровать сообщение";
            return false;
        }
        body = "{\"id\":\"" + Utf8(id) + "\",\"kind\":\"encrypted\",\"ciphertext\":\"" +
               Utf8(Base64UrlEncode(cipher)) + "\",\"iv\":\"" + Utf8(Base64UrlEncode(iv)) +
               "\",\"keyId\":\"" + Utf8(snapshot.keyId) + "\",\"version\":2}";
    } else {
        body = "{\"id\":\"" + Utf8(id) + "\",\"kind\":\"text\",\"content\":\"" +
               JsonEscape(text) + "\"}";
    }
    DWORD status = 0;
    return Request(target, L"POST", L"/api/rooms/" + target.room + L"/items", body,
                   true, nullptr, &status, error);
}
