#include "room_events.h"

#include <windows.h>
#include <winhttp.h>

#include <string>
#include <vector>

namespace {
constexpr DWORD kUpgradeOption = 114;
constexpr int kUtf8Message = 2;
constexpr int kUtf8Fragment = 3;
constexpr int kCloseBuffer = 4;

typedef HINTERNET (WINAPI *CompleteUpgradeFunction)(HINTERNET, DWORD_PTR);
typedef DWORD (WINAPI *ReceiveFunction)(HINTERNET, PVOID, DWORD, DWORD*, int*);
typedef DWORD (WINAPI *CloseFunction)(HINTERNET, USHORT, PVOID, DWORD);

struct Handle {
    HINTERNET value = nullptr;
    ~Handle() { if (value) WinHttpCloseHandle(value); }
};

bool Parse(const std::wstring& input, bool* secure, std::wstring* host,
           INTERNET_PORT* port, std::wstring* room) {
    std::wstring url = input.substr(0, input.find(L'#'));
    URL_COMPONENTSW components = {};
    components.dwStructSize = sizeof(components);
    components.dwHostNameLength = static_cast<DWORD>(-1);
    components.dwUrlPathLength = static_cast<DWORD>(-1);
    if (!WinHttpCrackUrl(url.c_str(), static_cast<DWORD>(url.size()), 0, &components)) return false;
    const std::wstring path(components.lpszUrlPath, components.dwUrlPathLength);
    if (path.compare(0, 3, L"/r/") != 0) return false;
    *room = path.substr(3);
    if (!room->empty() && room->back() == L'/') room->pop_back();
    *secure = components.nScheme == INTERNET_SCHEME_HTTPS;
    host->assign(components.lpszHostName, components.dwHostNameLength);
    *port = components.nPort;
    return !room->empty();
}
}

RoomEventResult WaitForRoomRefresh(const std::wstring& roomUrl) {
    HMODULE winhttp = GetModuleHandleW(L"winhttp.dll");
    const auto complete = winhttp ? reinterpret_cast<CompleteUpgradeFunction>(
        GetProcAddress(winhttp, "WinHttpWebSocketCompleteUpgrade")) : nullptr;
    const auto receive = winhttp ? reinterpret_cast<ReceiveFunction>(
        GetProcAddress(winhttp, "WinHttpWebSocketReceive")) : nullptr;
    const auto closeSocket = winhttp ? reinterpret_cast<CloseFunction>(
        GetProcAddress(winhttp, "WinHttpWebSocketClose")) : nullptr;
    if (!complete || !receive || !closeSocket) return RoomEventResult::Unsupported;

    bool secure = false;
    std::wstring host, room;
    INTERNET_PORT port = 0;
    if (!Parse(roomUrl, &secure, &host, &port, &room)) return RoomEventResult::Disconnected;
    Handle session, connection, request, socket;
    session.value = WinHttpOpen(L"ClipboardExchangeWin32/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY,
                                WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!session.value) return RoomEventResult::Disconnected;
    WinHttpSetTimeouts(session.value, 5000, 5000, 35000, 35000);
    connection.value = WinHttpConnect(session.value, host.c_str(), port, 0);
    if (!connection.value) return RoomEventResult::Disconnected;
    const std::wstring path = L"/api/rooms/" + room + L"/events";
    request.value = WinHttpOpenRequest(connection.value, L"GET", path.c_str(), nullptr,
                                       WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES,
                                       secure ? WINHTTP_FLAG_SECURE : 0);
    if (!request.value || !WinHttpSetOption(request.value, kUpgradeOption, nullptr, 0) ||
        !WinHttpSendRequest(request.value, WINHTTP_NO_ADDITIONAL_HEADERS, 0,
                            WINHTTP_NO_REQUEST_DATA, 0, 0, 0) ||
        !WinHttpReceiveResponse(request.value, nullptr)) return RoomEventResult::Disconnected;
    DWORD status = 0, statusBytes = sizeof(status);
    if (!WinHttpQueryHeaders(request.value, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER,
                             WINHTTP_HEADER_NAME_BY_INDEX, &status, &statusBytes,
                             WINHTTP_NO_HEADER_INDEX) || status != 101) return RoomEventResult::Disconnected;
    socket.value = complete(request.value, 0);
    if (!socket.value) return RoomEventResult::Disconnected;
    WinHttpCloseHandle(request.value);
    request.value = nullptr;

    std::string message;
    std::vector<char> buffer(2048);
    for (;;) {
        DWORD bytes = 0;
        int type = kCloseBuffer;
        const DWORD result = receive(socket.value, buffer.data(), static_cast<DWORD>(buffer.size()),
                                     &bytes, &type);
        if (result != NO_ERROR || type == kCloseBuffer) return RoomEventResult::Disconnected;
        if (type == kUtf8Fragment || type == kUtf8Message) {
            if (message.size() + bytes > 8192) return RoomEventResult::Disconnected;
            message.append(buffer.data(), bytes);
            if (type == kUtf8Message) {
                if (message.find("\"type\":\"refresh\"") != std::string::npos) {
                    closeSocket(socket.value, 1000, nullptr, 0);
                    return RoomEventResult::Refresh;
                }
                message.clear();
            }
        }
    }
}
