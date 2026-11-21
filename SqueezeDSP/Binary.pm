package Plugins::SqueezeDSP::Binary;
use strict;
# (SqueezeDSP fork, 0.1.72.46) the engine's path as setup_binary finds it (Page.pm's /squeezedsp/bench runs it)
our $ENGINE;
# (0.1.72.57) the engine's own config, Bin/SqueezeDSP_config.json, where it reads "debug" at each run
our $APPCONFIG;
use File::Copy;
use File::Spec::Functions qw(:ALL);
use Slim::Utils::Network;
use Slim::Utils::Prefs;
#use Plugins::SqueezeDSP::Plugin qw($log $thisapp $pluginDataDir $pluginSettingsDataDir $pluginImpulsesDataDir $pluginMatrixDataDir $pluginTempDataDir);

sub binaries {
    my $os = Slim::Utils::OSDetect::details();
    if ($os->{'os'} eq 'Linux') {
        if ($os->{'osArch'} =~ /x86_64/) {
            return qw(/publishLinux-x64/SqueezeDSP);
        }
        if ($os->{'binArch'} =~ /i386/) {
            return qw(/publishLinux-x86/SqueezeDSP);
        }
        if ($os->{'osArch'} =~ /aarch64/) {
            return qw(/publishlinux-arm64/SqueezeDSP);
        }
        if ($os->{'binArch'} =~ /armhf/) {
            return qw(/publishlinux-arm/SqueezeDSP);
        }
        if ($os->{'binArch'} =~ /arm/) {
            return qw(/publishlinux-arm/SqueezeDSP);
        }
        return qw(/publishLinux-x86/SqueezeDSP);
    }
    if ($os->{'os'} eq 'Unix') {
        if ($os->{'osName'} =~ /freebsd/) {
            return qw(/publishLinux-x64/SqueezeDSP);
        }
    }
    if ($os->{'os'} eq 'Darwin') {
        if ($os->{'osArch'} =~ /arm64/) {
            return qw(/publishOSX-arm64/SqueezeDSP);
        }
        return qw(/publishOSX-x64/SqueezeDSP);
    }
    if ($os->{'os'} eq 'Windows') {
        return qw(\publishWin64\SqueezeDSP.exe);
    }
}

sub setup_binary {
    my $class = shift;
    #my $bin = $class->binaries();
    my $bin = binaries();
    Plugins::SqueezeDSP::Utils::debug("Fox plugin path: " . $bin . " binary");
    my $exec = catdir(Slim::Utils::PluginManager->allPlugins->{$Plugins::SqueezeDSP::Plugin::thisapp}->{'basedir'}, 'Bin', $bin);
    Plugins::SqueezeDSP::Utils::debug("Fox plugin path: " . $exec . " exec");
    my $binExtension = ".exe";
    if (Slim::Utils::OSDetect::details()->{'os'} ne 'Windows') {
        $binExtension = "";
        chmod(0777, $Plugins::SqueezeDSP::Plugin::pluginImpulsesDataDir);
        chmod(0777, $Plugins::SqueezeDSP::Plugin::pluginTempDataDir);
    }
    if (!-e $exec) {
        $Plugins::SqueezeDSP::Plugin::log->warn("$exec not executable");
    }
    my $binpath = catdir(Slim::Utils::PluginManager->allPlugins->{$Plugins::SqueezeDSP::Plugin::thisapp}->{'basedir'}, 'Bin', "/", $Plugins::SqueezeDSP::Plugin::convolver . $binExtension);
    my $binversion_path = catdir(Slim::Utils::PluginManager->allPlugins->{$Plugins::SqueezeDSP::Plugin::thisapp}->{'basedir'}, 'Bin', "/", $Plugins::SqueezeDSP::Plugin::binversion);
    Plugins::SqueezeDSP::Utils::debug('standard binary path: ' . $binpath);
    $ENGINE = $binpath;
    if (-e $binversion_path) {
        Plugins::SqueezeDSP::Utils::debug('copying binary' . $exec);
        copy($exec, $binpath) or die "copy failed: $!";
        if ($binExtension eq "") {
            Plugins::SqueezeDSP::Utils::debug('executable not having \'x\' permission, correcting');
            chmod(0555, $binpath);
        }
        unlink $binversion_path;
        housekeeping();
    }
    my $appConfig = catdir(Slim::Utils::PluginManager->allPlugins->{$Plugins::SqueezeDSP::Plugin::thisapp}->{'basedir'}, 'Bin', "/", 'SqueezeDSP_config.json');
    $APPCONFIG = $appConfig;
    # the engine's debug log (Simon 2026-11-20: a switch on the pages, not a hand edit of this file): the plugin's pref
    # debug, written here at each start, so a plugin update (a new Bin folder, this file with it) keeps it; the first
    # start with the pref takes what the file holds, so a debug set by hand stays on
    my $was = Plugins::SqueezeDSP::Utils::LoadJSONFile($appConfig);
    my $fileDebug = ( ref $was eq 'HASH' && ref $was->{settings} eq 'HASH' && lc( $was->{settings}{debug} // '' ) eq 'true' ) ? 1 : 0;
    preferences('plugin.squeezedsp')->init({ debug => $fileDebug });
    amendPluginConfig($appConfig, 'debug', debugOn() ? 'true' : 'false');
    amendPluginConfig($appConfig, 'pluginDataFolder', $Plugins::SqueezeDSP::Plugin::pluginDataDir);
    amendPluginConfig($appConfig, 'settingsDataFolder', $Plugins::SqueezeDSP::Plugin::pluginSettingsDataDir);
    amendPluginConfig($appConfig, 'impulseDataFolder', $Plugins::SqueezeDSP::Plugin::pluginImpulsesDataDir);
    amendPluginConfig($appConfig, 'tempDataFolder', $Plugins::SqueezeDSP::Plugin::pluginTempDataDir);
    my $mybinary = Slim::Utils::Misc::findbin('sox');
    amendPluginConfig($appConfig, 'soxExe', $mybinary);
	my $mybinary = Slim::Utils::Misc::findbin('ffmpeg');
    amendPluginConfig($appConfig, 'ffmpegExe', $mybinary);
		my $mybinary = Slim::Utils::Misc::findbin('lame');
    amendPluginConfig($appConfig, 'lameExe', $mybinary);



    $Plugins::SqueezeDSP::Plugin::logfile = catdir(Slim::Utils::OSDetect::dirsFor('log'), "squeezedsp.log");
    if (! -e $Plugins::SqueezeDSP::Plugin::logfile) {
        open my $fh, '>', $Plugins::SqueezeDSP::Plugin::logfile or die "Could not create log file: $!";
        close $fh;
        Plugins::SqueezeDSP::Utils::debug('Log file created: ' . $Plugins::SqueezeDSP::Plugin::logfile);
    } else {
        Plugins::SqueezeDSP::Utils::debug('Log file already exists: ' . $Plugins::SqueezeDSP::Plugin::logfile);
    }
    amendPluginConfig($appConfig, 'logFile', $Plugins::SqueezeDSP::Plugin::logfile);
	# add LMS host and port to config for use by binary when calling back to server
	my $lmsHost = Slim::Utils::Network::serverAddr();
	my $serverPrefs = preferences('server');
	my $lmsPort = $serverPrefs->get('httpport') || 9000;
	amendPluginConfig($appConfig, 'lmsHost', $lmsHost);
	amendPluginConfig($appConfig, 'lmsPort', $lmsPort);
}

sub housekeeping {
    unlink glob catdir($Plugins::SqueezeDSP::Plugin::pluginTempDataDir, "/", '*.filter');
    unlink glob catdir($Plugins::SqueezeDSP::Plugin::pluginTempDataDir, "/", '*.filter.wav');
    unlink glob catdir($Plugins::SqueezeDSP::Plugin::pluginTempDataDir, "/", '*.wav');
	unlink glob catdir($Plugins::SqueezeDSP::Plugin::pluginTempDataDir, "/", '*.gob');
    unlink grep { !/\current.json/ } glob catdir($Plugins::SqueezeDSP::Plugin::pluginTempDataDir, "/", '*.json');
}

# the engine's debug log, as the plugin's pref holds it (1 on), and its switch: the pref and the engine's config both,
# so the next stream the engine starts (any player) logs at that level
sub debugOn {
    return preferences('plugin.squeezedsp')->get('debug') ? 1 : 0;
}

sub setDebug {
    my $on = shift() ? 1 : 0;
    preferences('plugin.squeezedsp')->set( 'debug', $on );
    amendPluginConfig( $APPCONFIG, 'debug', $on ? 'true' : 'false' ) if defined $APPCONFIG;
    return $on;
}

sub amendPluginConfig {
    my ($myJSONFile, $myKey, $myValue) = @_;
    Plugins::SqueezeDSP::Utils::debug('Fox amend file: ' . $myJSONFile . ' for key =' . $myKey . ' value= ' . $myValue);
    my $myConfig =  Plugins::SqueezeDSP::Utils::LoadJSONFile($myJSONFile);
    $myConfig->{settings}->{$myKey} = $myValue;
    Plugins::SqueezeDSP::Utils::SaveJSONFile($myConfig, $myJSONFile);
}

1;