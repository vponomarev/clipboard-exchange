#pragma once

#include <string>
#include <vector>

bool Base64UrlDecode(const std::wstring& value, std::vector<unsigned char>* bytes);
std::wstring Base64UrlEncode(const std::vector<unsigned char>& bytes);
bool Sha256(const std::vector<unsigned char>& input, std::vector<unsigned char>* digest);
bool RandomBytes(size_t count, std::vector<unsigned char>* bytes);
bool Aes256GcmEncrypt(const std::vector<unsigned char>& key,
                      const std::vector<unsigned char>& iv,
                      const std::vector<unsigned char>& aad,
                      const std::vector<unsigned char>& plaintext,
                      std::vector<unsigned char>* ciphertextAndTag);
bool Aes256GcmDecrypt(const std::vector<unsigned char>& key,
                      const std::vector<unsigned char>& iv,
                      const std::vector<unsigned char>& aad,
                      const std::vector<unsigned char>& ciphertextAndTag,
                      std::vector<unsigned char>* plaintext);
