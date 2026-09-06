#pragma once

#include <string>
#include <vector>

std::vector<std::wstring> LoadHistoryCache(const std::wstring& roomUrl);
bool SaveHistoryCache(const std::wstring& roomUrl, const std::vector<std::wstring>& messages);
