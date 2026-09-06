'use strict';

const fs = require('node:fs');
const path = require('node:path');

const source = path.resolve(__dirname, '../../assets/icon.png');
const destination = path.resolve(__dirname, '../resources/app.ico');
const png = fs.readFileSync(source);
const header = Buffer.alloc(22);
header.writeUInt16LE(0, 0);       // reserved
header.writeUInt16LE(1, 2);       // icon
header.writeUInt16LE(1, 4);       // one image
header.writeUInt8(0, 6);          // 256 px
header.writeUInt8(0, 7);          // 256 px
header.writeUInt8(0, 8);          // palette
header.writeUInt8(0, 9);          // reserved
header.writeUInt16LE(1, 10);      // color planes
header.writeUInt16LE(32, 12);     // bits per pixel
header.writeUInt32LE(png.length, 14);
header.writeUInt32LE(header.length, 18);
fs.writeFileSync(destination, Buffer.concat([header, png]));
