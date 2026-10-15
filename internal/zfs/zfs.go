// Package zfs arma y valida todo lo que TangoOS le pide a OpenZFS (ADR 0013,
// protocol/README.md §18): nombres de pools y datasets, formas de vdev, la estimación
// de capacidad del asistente, los argumentos de zpool/zfs y el parseo de su salida
// JSON (`-j`, OpenZFS 2.3).
//
// Nada de este paquete ejecuta comandos: el agente (root) arma el argv con estas
// funciones y lo corre; lo que llega desde la red nunca se concatena en un shell ni
// pasa una propiedad que no esté en la allow-list.
package zfs

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Binarios de zfsutils-linux en Debian.
const (
	ZpoolBin = "/usr/sbin/zpool"
	ZfsBin   = "/usr/sbin/zfs"
)

// Límites de lo que ofrece TangoOS (no de OpenZFS).
const (
	MaxPoolDisks = 24
	MaxNameLen   = 32
	// MinDiskSize: OpenZFS rechaza vdevs de menos de 64 MiB.
	MinDiskSize = 64 << 20
)

// Propiedades del pool y del dataset raíz que pone TangoOS al crear (docs/04 §ZFS).
var (
	PoolProps = []string{"ashift=12", "autotrim=on"}
	RootProps = []string{
		"compression=lz4", "atime=off", "xattr=sa", "acltype=posixacl", "dnodesize=auto",
		"canmount=off", "mountpoint=none",
	}
)

var (
	poolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	// Volumen de TangoOS = dataset <pool>/<volumen> montado en /<volumen>, mismo
	// formato que los demás volúmenes (diskops.ValidVolumeName).
	volNameRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)
	// Carpeta compartida / app: hijo de <pool>/<volumen>/shares o /apps.
	childNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	zvolNameRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	snapNameRe  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}Z(\.(hourly|daily|weekly))?$`)
	// byid: nombre de /dev/disk/by-id (sin el prefijo), lo que se le pasa a zpool.
	byIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:+-]{0,190}$`)
	guidRe = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// Palabras que zpool interpreta como tipo de vdev o que el kernel reserva.
var reservedPool = map[string]bool{
	"mirror": true, "raidz": true, "raidz1": true, "raidz2": true, "raidz3": true, "draid": true,
	"spare": true, "log": true, "cache": true, "special": true, "dedup": true, "all": true, "none": true,
}

// Directorios del sistema que nunca pueden ser /<volumen> (como diskops.reservedNames).
var reservedVol = map[string]bool{
	"bin": true, "boot": true, "dev": true, "etc": true, "home": true, "lib": true, "lib32": true, "lib64": true,
	"libx32": true, "media": true, "mnt": true, "opt": true, "proc": true, "root": true, "run": true, "sbin": true,
	"srv": true, "sys": true, "tmp": true, "usr": true, "var": true, "import": true, "lost": true, "snap": true,
	"cluster": true,
}

// ValidPoolName valida el nombre de un pool. Sin guiones ni mayúsculas: así nunca
// empieza con "-" ni choca con las palabras de vdev (mirror, raidz…).
func ValidPoolName(name string) error {
	if !poolNameRe.MatchString(name) {
		return fmt.Errorf("el nombre del pool debe empezar con una letra y tener solo minúsculas, números o _ (máx. %d)", MaxNameLen)
	}
	if reservedPool[name] || strings.HasPrefix(name, "raidz") || strings.HasPrefix(name, "draid") || strings.HasPrefix(name, "mirror") ||
		(len(name) > 1 && name[0] == 'c' && name[1] >= '0' && name[1] <= '9') {
		return fmt.Errorf("«%s» es una palabra reservada de ZFS", name)
	}
	return nil
}

// ValidVolumeName valida el nombre de un volumen ZFS de TangoOS (= punto de montaje).
func ValidVolumeName(name string) error {
	if !volNameRe.MatchString(name) {
		return fmt.Errorf("el nombre debe empezar con una letra y tener solo minúsculas y números (máx. 32)")
	}
	if reservedVol[name] {
		return fmt.Errorf("«%s» es un directorio del sistema", name)
	}
	return nil
}

// ValidZvolName valida el nombre de un volumen de bloque (zvol) para iSCSI.
func ValidZvolName(name string) error {
	if !zvolNameRe.MatchString(name) {
		return fmt.Errorf("el nombre del volumen de bloque debe empezar con una letra y tener minúsculas, números, - o _ (máx. 32)")
	}
	return nil
}

// ValidChildName valida una carpeta compartida o una app dentro de un volumen ZFS.
func ValidChildName(name string) error {
	if !childNameRe.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("nombre inválido")
	}
	return nil
}

// ValidSnapName acepta solo los nombres de instantánea de TangoOS (hora UTC + nivel).
func ValidSnapName(name string) error {
	if !snapNameRe.MatchString(name) {
		return fmt.Errorf("nombre de instantánea inválido")
	}
	return nil
}

// ValidByID valida un nombre de /dev/disk/by-id.
func ValidByID(id string) error {
	if !byIDRe.MatchString(id) || strings.Contains(id, "..") {
		return fmt.Errorf("disco inválido: %q", id)
	}
	return nil
}

// ValidGUID valida el GUID numérico de un pool (zpool import).
func ValidGUID(g string) error {
	if !guidRe.MatchString(g) {
		return fmt.Errorf("guid inválido")
	}
	return nil
}

// ---- Datasets ---------------------------------------------------------------------

// VolumeDataset es el dataset de un volumen ZFS de TangoOS.
func VolumeDataset(pool, vol string) string { return pool + "/" + vol }

// SharesDataset y AppsDataset son los padres (canmount=off) de carpetas y apps: sus
// hijos heredan el punto de montaje /<vol>/@shares/<X> y /<vol>/@apps/<X>, el mismo
// layout que en Btrfs, así Samba, permisos, Archivos e instantáneas no cambian.
func SharesDataset(volDS string) string { return volDS + "/shares" }
func AppsDataset(volDS string) string   { return volDS + "/apps" }

// ChildDataset traduce una ruta de subvolumen de TangoOS ("@shares/Fotos",
// "@apps/jellyfin") al dataset hijo del volumen ZFS.
func ChildDataset(volDS, subvol string) (string, error) {
	top, name, ok := strings.Cut(subvol, "/")
	if !ok || ValidChildName(name) != nil {
		return "", fmt.Errorf("ruta inválida: %q", subvol)
	}
	switch top {
	case "@shares":
		return SharesDataset(volDS) + "/" + name, nil
	case "@apps":
		return AppsDataset(volDS) + "/" + name, nil
	}
	return "", fmt.Errorf("ruta inválida: %q", subvol)
}

// ZvolDataset es el dataset de un volumen de bloque: <pool>/zvol/<nombre>.
func ZvolDataset(pool, name string) string { return pool + "/zvol/" + name }

// ZvolDevice es el nodo que crea udev para un zvol (lo usa el LUN de iSCSI).
func ZvolDevice(pool, name string) string { return "/dev/zvol/" + ZvolDataset(pool, name) }

// ---- Formas de vdev y capacidad ------------------------------------------------------

// Layouts que ofrece TangoOS.
var Layouts = []string{"stripe", "mirror", "raidz1", "raidz2", "raidz3"}

// Parity devuelve los discos de redundancia de una forma.
func Parity(layout string) int {
	switch layout {
	case "raidz1":
		return 1
	case "raidz2":
		return 2
	case "raidz3":
		return 3
	}
	return 0
}

// MinDisks es la cantidad mínima de discos por forma. raidz con un disco de datos
// funciona pero no tiene sentido: se pide al menos paridad + 2.
func MinDisks(layout string) int {
	switch layout {
	case "stripe":
		return 1
	case "mirror":
		return 2
	case "raidz1":
		return 3
	case "raidz2":
		return 4
	case "raidz3":
		return 5
	}
	return 0
}

// CheckLayout valida la forma y la cantidad de discos de un vdev.
func CheckLayout(layout string, n int) error {
	min := MinDisks(layout)
	if min == 0 {
		return fmt.Errorf("forma de vdev desconocida: %q", layout)
	}
	if n < min {
		return fmt.Errorf("%s necesita al menos %d discos", LayoutLabel(layout), min)
	}
	if n > MaxPoolDisks {
		return fmt.Errorf("como máximo %d discos por vdev", MaxPoolDisks)
	}
	return nil
}

// LayoutLabel es el nombre que ve el usuario.
func LayoutLabel(layout string) string {
	switch layout {
	case "stripe":
		return "Sin redundancia (stripe)"
	case "mirror":
		return "Espejo"
	case "raidz1":
		return "RAIDZ1"
	case "raidz2":
		return "RAIDZ2"
	case "raidz3":
		return "RAIDZ3"
	}
	return layout
}

// Tolerance es cuántos discos puede perder un vdev sin perder datos.
func Tolerance(layout string, n int) int {
	switch layout {
	case "mirror":
		return n - 1
	case "stripe":
		return 0
	}
	return Parity(layout)
}

// Usable estima la capacidad útil de un vdev en bytes: cada disco cuenta como el
// más chico (ZFS no aprovecha la diferencia), menos la paridad. No descuenta la
// reserva interna de ZFS (~3,2 %, slop space) ni el relleno de raidz: el asistente lo
// muestra como "aprox.".
func Usable(layout string, sizes []uint64) uint64 {
	if len(sizes) == 0 {
		return 0
	}
	min := sizes[0]
	for _, s := range sizes[1:] {
		if s < min {
			min = s
		}
	}
	n := uint64(len(sizes))
	switch layout {
	case "stripe":
		var sum uint64
		for _, s := range sizes {
			sum += s
		}
		return sum
	case "mirror":
		return min
	}
	p := uint64(Parity(layout))
	if n <= p {
		return 0
	}
	return min * (n - p)
}

// Wasted es lo que no se aprovecha de los discos más grandes de un vdev.
func Wasted(layout string, sizes []uint64) uint64 {
	if layout == "stripe" || len(sizes) == 0 {
		return 0
	}
	min := sizes[0]
	for _, s := range sizes[1:] {
		if s < min {
			min = s
		}
	}
	var w uint64
	for _, s := range sizes {
		w += s - min
	}
	return w
}

// ---- Argumentos de zpool / zfs --------------------------------------------------------

// VdevArgs arma "mirror /dev/disk/by-id/a /dev/disk/by-id/b" (stripe: solo los discos).
func VdevArgs(layout string, byIDs []string) ([]string, error) {
	if err := CheckLayout(layout, len(byIDs)); err != nil {
		return nil, err
	}
	var a []string
	if layout != "stripe" {
		a = append(a, layout)
	}
	seen := map[string]bool{}
	for _, id := range byIDs {
		if err := ValidByID(id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("disco repetido: %s", id)
		}
		seen[id] = true
		a = append(a, "/dev/disk/by-id/"+id)
	}
	return a, nil
}

// ArgsPoolCreate: zpool create -o ashift=12 … -O compression=lz4 … <pool> <vdev>.
// -f porque el agente ya borró las firmas viejas de discos que verificó libres.
func ArgsPoolCreate(pool, layout string, byIDs []string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	vdev, err := VdevArgs(layout, byIDs)
	if err != nil {
		return nil, err
	}
	a := []string{"create", "-f"}
	for _, p := range PoolProps {
		a = append(a, "-o", p)
	}
	for _, p := range RootProps {
		a = append(a, "-O", p)
	}
	a = append(a, pool)
	return append(a, vdev...), nil
}

// ArgsPoolAdd agrega un vdev de datos (mismo tipo que el pool, lo valida tangod).
func ArgsPoolAdd(pool, layout string, byIDs []string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	vdev, err := VdevArgs(layout, byIDs)
	if err != nil {
		return nil, err
	}
	return append([]string{"add", "-f", pool}, vdev...), nil
}

// ArgsAuxAdd agrega caché de lectura (L2ARC, "cache") o registro de escritura
// sincrónica (SLOG, "log"; en espejo si son dos SSD).
func ArgsAuxAdd(pool, kind string, byIDs []string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if kind != "cache" && kind != "log" {
		return nil, fmt.Errorf("tipo inválido: %q", kind)
	}
	if len(byIDs) < 1 || len(byIDs) > 2 {
		return nil, fmt.Errorf("uno o dos SSD")
	}
	a := []string{"add", "-f", pool, kind}
	if kind == "log" && len(byIDs) == 2 {
		a = append(a, "mirror")
	}
	for _, id := range byIDs {
		if err := ValidByID(id); err != nil {
			return nil, err
		}
		a = append(a, "/dev/disk/by-id/"+id)
	}
	return a, nil
}

// ArgsRemove quita un dispositivo de caché o de registro (por su nombre de vdev).
func ArgsRemove(pool, vdev string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if err := ValidVdevName(vdev); err != nil {
		return nil, err
	}
	return []string{"remove", pool, vdev}, nil
}

var vdevNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:+-]{0,190}$`)

// ValidVdevName valida un nombre de vdev como lo muestra zpool status (by-id o
// "mirror-1").
func ValidVdevName(v string) error {
	if !vdevNameRe.MatchString(v) || strings.Contains(v, "..") {
		return fmt.Errorf("vdev inválido: %q", v)
	}
	return nil
}

// ArgsReplace: zpool replace <pool> <viejo> /dev/disk/by-id/<nuevo>. "viejo" es el
// nombre del vdev (by-id) o su GUID si el disco ya no está.
func ArgsReplace(pool, old, newByID string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if err := ValidVdevName(old); err != nil {
		return nil, err
	}
	if err := ValidByID(newByID); err != nil {
		return nil, err
	}
	return []string{"replace", "-f", pool, old, "/dev/disk/by-id/" + newByID}, nil
}

// ArgsScrub: zpool scrub [-s] <pool>.
func ArgsScrub(pool string, stop bool) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if stop {
		return []string{"scrub", "-s", pool}, nil
	}
	return []string{"scrub", pool}, nil
}

// ArgsImport importa por nombre o GUID. readonly: el primer paso que ofrece la UI con
// pools de otro equipo; -N no monta nada (TangoOS decide qué es un volumen).
func ArgsImport(nameOrGUID, newName string, readonly bool) ([]string, error) {
	if ValidPoolName(nameOrGUID) != nil && ValidGUID(nameOrGUID) != nil {
		return nil, fmt.Errorf("pool inválido")
	}
	a := []string{"import", "-d", "/dev/disk/by-id", "-N"}
	if readonly {
		a = append(a, "-o", "readonly=on")
	}
	a = append(a, nameOrGUID)
	if newName != "" {
		if err := ValidPoolName(newName); err != nil {
			return nil, err
		}
		a = append(a, newName)
	}
	return a, nil
}

// ArgsExport y ArgsDestroy.
func ArgsExport(pool string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	return []string{"export", pool}, nil
}

func ArgsDestroy(pool string) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	return []string{"destroy", "-f", pool}, nil
}

// VolumeOpts son las opciones de un volumen ZFS (dataset montado en /<vol>).
type VolumeOpts struct {
	Quota       uint64 // bytes, 0 = sin límite
	Reservation uint64 // bytes, 0 = sin reserva
	Encrypted   bool   // passphrase por stdin (keylocation=prompt)
}

// ArgsVolumeCreate: zfs create -u -o mountpoint=/<vol> … <pool>/<vol>. -u: el agente
// lo crea sin montar (corre en su propio namespace) y el montaje lo hace la unidad
// transitoria en el del sistema.
func ArgsVolumeCreate(pool, vol string, o VolumeOpts) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if err := ValidVolumeName(vol); err != nil {
		return nil, err
	}
	a := []string{"create", "-u", "-o", "mountpoint=/" + vol, "-o", "canmount=on"}
	a = append(a, sizeProps(o.Quota, o.Reservation)...)
	if o.Encrypted {
		a = append(a, "-o", "encryption=aes-256-gcm", "-o", "keyformat=passphrase", "-o", "keylocation=prompt")
	}
	return append(a, VolumeDataset(pool, vol)), nil
}

func sizeProps(quota, reservation uint64) []string {
	var a []string
	if quota > 0 {
		a = append(a, "-o", "quota="+strconv.FormatUint(quota, 10))
	}
	if reservation > 0 {
		a = append(a, "-o", "reservation="+strconv.FormatUint(reservation, 10))
	}
	return a
}

// ArgsParentCreate crea shares/ o apps/ (sin montar, heredan el cifrado del volumen).
func ArgsParentCreate(volDS, mount, kind string) ([]string, error) {
	var ds, mp string
	switch kind {
	case "shares":
		ds, mp = SharesDataset(volDS), mount+"/@shares"
	case "apps":
		ds, mp = AppsDataset(volDS), mount+"/@apps"
	default:
		return nil, fmt.Errorf("tipo inválido")
	}
	return []string{"create", "-u", "-o", "canmount=off", "-o", "mountpoint=" + mp, ds}, nil
}

// ArgsChildCreate crea una carpeta compartida o app (hereda punto de montaje).
func ArgsChildCreate(childDS string) []string { return []string{"create", "-u", childDS} }

// ArgsSetSize cambia cuota y reserva (0 = none).
func ArgsSetSize(ds string, quota, reservation uint64) []string {
	q, r := "none", "none"
	if quota > 0 {
		q = strconv.FormatUint(quota, 10)
	}
	if reservation > 0 {
		r = strconv.FormatUint(reservation, 10)
	}
	return []string{"set", "quota=" + q, "reservation=" + r, ds}
}

// ArgsZvolCreate: volumen de bloque para iSCSI (volblocksize 16K, el valor por
// defecto de OpenZFS 2.2+); sparse = sin reserva (thin).
func ArgsZvolCreate(pool, name string, size uint64, sparse bool) ([]string, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if err := ValidZvolName(name); err != nil {
		return nil, err
	}
	if size < 64<<20 || size%(1<<20) != 0 {
		return nil, fmt.Errorf("el tamaño tiene que ser de al menos 64 MiB y múltiplo de 1 MiB")
	}
	a := []string{"create", "-p"}
	if sparse {
		a = append(a, "-s")
	}
	return append(a, "-V", strconv.FormatUint(size, 10), ZvolDataset(pool, name)), nil
}

// ArgsSnapshot / ArgsSnapDestroy sobre el dataset de una carpeta.
func ArgsSnapshot(ds, name string) ([]string, error) {
	if err := ValidSnapName(name); err != nil {
		return nil, err
	}
	return []string{"snapshot", ds + "@" + name}, nil
}

func ArgsSnapDestroy(ds, name string) ([]string, error) {
	if err := ValidSnapName(name); err != nil {
		return nil, err
	}
	return []string{"destroy", ds + "@" + name}, nil
}

// ---- Utilidades ----------------------------------------------------------------------

// SortedKeys devuelve las claves de un mapa ordenadas (salidas determinísticas).
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PoolOfDataset devuelve el pool de un dataset ("tank/volume3/shares/X" → "tank").
func PoolOfDataset(ds string) string {
	p, _, _ := strings.Cut(ds, "/")
	return p
}
