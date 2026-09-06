using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Windows.Automation;

internal static class ClipboardExchangeHelper
{
    [StructLayout(LayoutKind.Sequential)]
    private struct Input
    {
        public uint Type;
        public InputUnion Data;
    }

    [StructLayout(LayoutKind.Explicit)]
    private struct InputUnion
    {
        [FieldOffset(0)] public KeyboardInput Keyboard;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct KeyboardInput
    {
        public ushort VirtualKey;
        public ushort ScanCode;
        public uint Flags;
        public uint Time;
        public UIntPtr ExtraInfo;
    }

    [DllImport("user32.dll", SetLastError = true)]
    private static extern uint SendInput(uint count, Input[] inputs, int size);

    private const uint Keyboard = 1;
    private const uint KeyUp = 0x0002;
    private const uint Unicode = 0x0004;

    [STAThread]
    private static int Main(string[] args)
    {
        try
        {
            if (args.Length == 1 && args[0] == "selection")
            {
                WriteSelection();
                return 0;
            }
            if (args.Length == 2 && args[0] == "type")
            {
                string text = Encoding.UTF8.GetString(Convert.FromBase64String(args[1]));
                Thread.Sleep(80);
                TypeText(text);
                return 0;
            }
            Console.Error.WriteLine("usage: helper selection | helper type BASE64");
            return 2;
        }
        catch (Exception error)
        {
            Console.Error.WriteLine(error.Message);
            return 1;
        }
    }

    private static void WriteSelection()
    {
        Thread.Sleep(40);
        AutomationElement element = AutomationElement.FocusedElement;
        object value;
        if (element == null || !element.TryGetCurrentPattern(TextPattern.Pattern, out value))
            throw new InvalidOperationException("The focused application does not expose a UI Automation text selection");
        var text = new StringBuilder();
        foreach (TextPatternRange range in ((TextPattern)value).GetSelection()) text.Append(range.GetText(-1));
        Console.Write(Convert.ToBase64String(Encoding.UTF8.GetBytes(text.ToString())));
    }

    private static Input Key(ushort virtualKey, ushort scanCode, uint flags)
    {
        return new Input
        {
            Type = Keyboard,
            Data = new InputUnion { Keyboard = new KeyboardInput { VirtualKey = virtualKey, ScanCode = scanCode, Flags = flags } }
        };
    }

    private static void TypeText(string text)
    {
        var inputs = new List<Input>(text.Length * 2);
        foreach (char value in text)
        {
            if (value == '\r') continue;
            if (value == '\n')
            {
                inputs.Add(Key(0x0D, 0, 0));
                inputs.Add(Key(0x0D, 0, KeyUp));
            }
            else
            {
                inputs.Add(Key(0, value, Unicode));
                inputs.Add(Key(0, value, Unicode | KeyUp));
            }
        }
        if (inputs.Count == 0) return;
        Input[] values = inputs.ToArray();
        uint delivered = SendInput((uint)values.Length, values, Marshal.SizeOf(typeof(Input)));
        if (delivered != values.Length) throw new InvalidOperationException("SendInput did not deliver every key event");
    }
}
