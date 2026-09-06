#include "room_client.h"

#include <iostream>

int wmain(int argc, wchar_t** argv) {
    if (argc != 2) {
        std::cerr << "usage: room-client-smoke ROOM_URL\n";
        return 2;
    }
    const std::wstring expected = L"Win32 smoke: Привет, 世界 \U0001F600 \U0001D11E\nsecond line";
    std::wstring error;
    if (!SendRoomText(argv[1], expected, &error)) {
        std::wcerr << L"send failed: " << error << L"\n";
        return 1;
    }
    RoomSnapshot snapshot;
    if (!FetchRoom(argv[1], &snapshot, &error)) {
        std::wcerr << L"fetch failed: " << error << L"\n";
        return 1;
    }
    if (snapshot.messages.empty() || snapshot.messages.front() != expected) {
        std::cerr << "round trip changed the message\n";
        return 1;
    }
    std::cout << "native WinHTTP room round trip passed\n";
    return 0;
}
