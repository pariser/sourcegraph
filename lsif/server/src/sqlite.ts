import * as lsp from 'vscode-languageserver'
import * as path from 'path'
import { Backend, NoLSIFDataError, QueryRunner } from './backend'
import { fs } from 'mz'
import { Database } from './ms/server/database'
import { InsertStats, QueryStats, instrument, CreateRunnerStats } from './stats'
import { readEnvInt } from './env'

/**
 * Where on the file system to store LSIF files.
 */
const STORAGE_ROOT = process.env.LSIF_STORAGE_ROOT || 'lsif-storage'

/**
 * Soft limit on the amount of storage used by LSIF files. Storage can exceed
 * this limit if a single LSIF file is larger than this, otherwise storage will
 * be kept under this limit. Defaults to 100GB.
 */
const SOFT_MAX_STORAGE = readEnvInt({ key: 'LSIF_SOFT_MAX_STORAGE', defaultValue: 100 * 1024 * 1024 * 1024 })

/**
 * The abstract SQLite backend base that supports graph and blob subclasses.
 */
export abstract class SQLiteBackend implements Backend<SQLiteQueryRunner> {
    /**
     * Read the content of the temporary file containing a JSON-encoded LSIF
     * dump. Insert these contents into some storage with an encoding that
     * can be subsequently read by the `createRunner` method.
     */
    public async insertDump(
        tempPath: string,
        repository: string,
        commit: string,
        contentLength: number
    ): Promise<{ insertStats: InsertStats }> {
        await this.createStorageRoot()
        const outFile = makeFilename(repository, commit)

        const { processStats } = await instrument(async () => {
            await this.convert(tempPath, outFile)
        })

        this.cleanStorageRoot(Math.max(0, SOFT_MAX_STORAGE - contentLength))
        const fileStat = await fs.stat(outFile)

        return {
            insertStats: {
                processStats,
                diskKb: fileStat.size / 1000,
            },
        }
    }

    /**
     * Lists the query methods available from this backend.
     */
    public availableQueries(): string[] {
        return ['definitions', 'hover', 'references']
    }

    /**
     * Create a query runner relevant to the given repository and commit hash. This
     * assumes that data for this subset of data has already been inserted via
     * `insertDump` (otherwise this method is expected to throw).
     */
    public async createRunner(
        repository: string,
        commit: string
    ): Promise<{ queryRunner: SQLiteQueryRunner; createRunnerStats: CreateRunnerStats }> {
        const file = makeFilename(repository, commit)

        try {
            await fs.stat(file)
        } catch (e) {
            if ('code' in e && e.code === 'ENOENT') {
                throw new NoLSIFDataError(repository, commit)
            }

            throw e
        }

        const db = this.createStore()

        const { processStats } = await instrument(async () => {
            return await db.load(file, root => ({
                toDatabase: path => `${root}/${path}`,
                fromDatabase: uri => (uri.startsWith(root) ? uri.slice(`${root}/`.length) : uri),
            }))
        })

        return {
            queryRunner: new SQLiteQueryRunner(db),
            createRunnerStats: { processStats },
        }
    }

    /**
     * Free any resources used by this object.
     */
    public close(): Promise<void> {
        return Promise.resolve()
    }

    /**
     * Ensure the storage root directory exists.
     */
    private async createStorageRoot(): Promise<void> {
        try {
            await fs.mkdir(STORAGE_ROOT)
        } catch (e) {
            if (e.code === 'EEXIST') {
                return
            }

            throw e
        }
    }

    /**
     * Deletes old files (sorted by last modified time) to keep the disk usage below
     * the given `max`.
     */
    private async cleanStorageRoot(max: number): Promise<void> {
        // TODO(efritz) - early-out

        const entries = await fs.readdir(STORAGE_ROOT)
        const files = await Promise.all(
            entries.map(async f => {
                const realPath = path.join(STORAGE_ROOT, f)
                return { path: realPath, stat: await fs.stat(realPath) }
            })
        )

        let totalSize = files.reduce((subtotal, f) => subtotal + f.stat.size, 0)

        // TODO - come up with a better fair-eviction policy so that one repo
        // with a  lot of dumps do not starve out disk space for other repos.

        for (const f of files.sort((a, b) => a.stat.atimeMs - b.stat.atimeMs)) {
            if (totalSize <= max) {
                return
            }

            console.log(`Deleting ${f.path} to help keep disk usage under ${SOFT_MAX_STORAGE}.`)
            await fs.unlink(f.path)
            totalSize = totalSize - f.stat.size
        }
    }

    /**
     * Generate a SQLite dump from a temporary file to the given target file.
     */
    protected abstract convert(inFile: string, outFile: string): Promise<void>

    /**
     * Create a new, empty Database. This object should be able to load the file
     * created by `buildCommand`.
     */
    protected abstract createStore(): Database
}

export class SQLiteQueryRunner implements QueryRunner {
    constructor(private db: Database) {}

    /**
     * Determines whether or not data exists for the given file.
     */
    public exists(file: string): Promise<boolean> {
        return Promise.resolve(Boolean(this.db.stat(file)))
    }

    /**
     * Return data for an LSIF query.
     */
    public async query(
        method: string,
        uri: string,
        position: lsp.Position
    ): Promise<{ result: any; queryStats: QueryStats }> {
        const { result, processStats } = await instrument(async () => {
            switch (method) {
                case 'hover':
                    return Promise.resolve(this.db.hover(uri, position))
                case 'definitions':
                    return Promise.resolve(this.db.definitions(uri, position))
                case 'references':
                    return Promise.resolve(this.db.references(uri, position, { includeDeclaration: true }))
                default:
                    throw new Error(`Unimplemented method ${method}`)
            }
        })

        return Promise.resolve({
            result,
            queryStats: { processStats },
        })
    }

    /**
     * Free any resources used by this object.
     */
    public close(): Promise<void> {
        this.db.close()
        return Promise.resolve()
    }
}

/**
 *.Computes the filename of the LSIF dump from the given repository and commit hash.
 */
function makeFilename(repository: string, commit: string): string {
    return path.join(STORAGE_ROOT, `${encodeURIComponent(repository)}@${commit}.lsif.db`)
}
