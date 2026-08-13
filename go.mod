module github.com/NextendoNetwork/super-mario-maker-2

go 1.23.0

require github.com/NextendoNetwork/nextendo-nex v0.1.4

require (
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/lxzan/gws v1.10.0 // indirect
)

// TEMPORARY (diagnóstico de la subida de niveles): usa el código local con logging PRUDP
// extra (ACK/RETRANSMIT) en vez de la versión publicada v0.1.4. Sacar este replace una vez
// resuelto el "Upload failed" — o si nextendo-nex se publica con estos cambios, actualizar
// el require de arriba y borrar esta línea.
replace github.com/NextendoNetwork/nextendo-nex => ../nextendo-nex
