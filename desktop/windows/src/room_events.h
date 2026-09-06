#pragma once

#include <string>

enum class RoomEventResult { Refresh, Disconnected, Unsupported };

RoomEventResult WaitForRoomRefresh(const std::wstring& roomUrl);
