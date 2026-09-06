#pragma once

#include <string>

// Extracts and validates a direct room URL or clipboard-exchange://connect URL
// from the argument tail supplied to wWinMain.
std::wstring ExtractRoomArgument(const std::wstring& commandLine);
