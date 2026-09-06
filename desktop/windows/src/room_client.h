#pragma once

#include <string>
#include <vector>

struct RoomSnapshot {
    std::vector<std::wstring> messages;
    bool writeProtected = false;
    bool encrypted = false;
    bool canWrite = false;
    std::wstring keyId;
};

bool ValidateRoomUrl(const std::wstring& roomUrl, std::wstring* error);
bool FetchRoom(const std::wstring& roomUrl, RoomSnapshot* snapshot, std::wstring* error);
bool SendRoomText(const std::wstring& roomUrl, const std::wstring& text, std::wstring* error);
