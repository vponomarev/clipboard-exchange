#include "room_events.h"

#include <iostream>

int wmain(int argc, wchar_t** argv) {
    if (argc != 2) return 2;
    const RoomEventResult result = WaitForRoomRefresh(argv[1]);
    if (result != RoomEventResult::Refresh) {
        std::cerr << "realtime event wait failed\n";
        return 1;
    }
    std::cout << "native WinHTTP WebSocket refresh passed\n";
    return 0;
}
