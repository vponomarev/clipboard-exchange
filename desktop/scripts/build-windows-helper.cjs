'use strict';

const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

if (process.platform === 'win32') {
  const windows = process.env.WINDIR || 'C:\\Windows';
  const candidates = [
    path.join(windows, 'Microsoft.NET', 'Framework64', 'v4.0.30319', 'csc.exe'),
    path.join(windows, 'Microsoft.NET', 'Framework', 'v4.0.30319', 'csc.exe')
  ];
  const compiler = candidates.find((candidate) => fs.existsSync(candidate));
  if (!compiler) throw new Error('Windows .NET Framework C# compiler was not found');
  const source = path.resolve(__dirname, '..', 'native', 'windows', 'ClipboardExchangeHelper.cs');
  const outputDirectory = path.resolve(__dirname, '..', 'native', 'bin');
  const output = path.join(outputDirectory, 'clipboard-exchange-windows-helper.exe');
  const referenceDirectory = path.join(process.env.ProgramFiles || 'C:\\Program Files', 'Reference Assemblies', 'Microsoft', 'Framework', 'v3.0');
  const automationClient = path.join(referenceDirectory, 'UIAutomationClient.dll');
  const automationTypes = path.join(referenceDirectory, 'UIAutomationTypes.dll');
  if (!fs.existsSync(automationClient) || !fs.existsSync(automationTypes)) throw new Error('Windows UI Automation reference assemblies were not found');
  fs.mkdirSync(outputDirectory, { recursive: true });
  execFileSync(compiler, [
    '/nologo', '/target:exe', '/optimize+', '/platform:anycpu',
    `/out:${output}`, `/reference:${automationClient}`, `/reference:${automationTypes}`, source
  ], { stdio: 'inherit' });
}
