// Batch Lua 5.1 compiler on top of LuaJ 2.0.3 (luaj-jse, MIT; its LuaC is a port of PUC Lua 5.1's
// lparser/lcode). serverdata_lua.py --verify uses it to recompile decompiled scripts and compare the
// result with the original chunks instruction by instruction.
//
// Runs in Java's single-file source mode, so nothing needs building:
//     java -cp <luaj-jse-2.0.3.jar> re/scripts/LuajCompileBatch.java <list.tsv>
// list.tsv: one "<input .lua>\t<output chunk>" per line. Output chunks are stripped, little endian,
// with 8-byte double numbers (LuaJ can't write 4-byte floats; the comparison rounds constants).
// One result line per input on stdout:  OK<TAB><input>   or   ERR<TAB><input><TAB><message>
import java.io.ByteArrayInputStream;
import java.io.FileOutputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import org.luaj.vm2.Prototype;
import org.luaj.vm2.compiler.DumpState;
import org.luaj.vm2.compiler.LuaC;

public class LuajCompileBatch {
    public static void main(String[] args) throws Exception {
        if (args.length != 1) {
            System.err.println("usage: java -cp luaj-jse.jar LuajCompileBatch.java <list.tsv>");
            System.exit(2);
        }
        List<String> lines = Files.readAllLines(Path.of(args[0]), StandardCharsets.UTF_8);
        for (String line : lines) {
            if (line.isBlank()) continue;
            String[] io = line.split("\t");
            try {
                byte[] src = Files.readAllBytes(Path.of(io[0]));
                Prototype p = LuaC.compile(new ByteArrayInputStream(src), "=chunk");
                try (OutputStream out = new FileOutputStream(io[1])) {
                    DumpState.dump(p, out, true, DumpState.NUMBER_FORMAT_FLOATS_OR_DOUBLES, true);
                }
                System.out.println("OK\t" + io[0]);
            } catch (Throwable t) {
                String msg = String.valueOf(t.getMessage()).replace('\t', ' ').replace('\n', ' ');
                System.out.println("ERR\t" + io[0] + "\t" + t.getClass().getSimpleName() + ": " + msg);
                try {
                    Files.deleteIfExists(Path.of(io[1]));
                } catch (Exception e) {
                    // a leftover partial output is ignored by the caller
                }
            }
            System.out.flush();
        }
    }
}
